#!/usr/bin/env bash
# scripts/step10_rto_test.sh
#
# Step 10 RTO validation and controlled failover test.
# Runs two sequential tests:
#
#   Test A — API pod recovery (RTO):
#     Deletes the API pod and measures time until the replacement pod
#     passes its readiness probe. Pass criterion: ≤ 60 seconds.
#
#   Test B — Controlled region failover:
#     Marks the current preferred region as 'down' in Atlas,
#     triggers a router refresh, confirms routing moved to a healthy
#     alternative, then restores the region.
#
# Evidence is written to deployments/chaos/evidence/step10/
#
# Prerequisites:
#   - step10_deploy.sh completed, all pods Running
#   - kubectl port-forwards for api (8080) and router (8084) are active:
#       kubectl port-forward -n resiliency svc/api 8080:8080 &
#       kubectl port-forward -n resiliency svc/router 8084:8084 &
#   - mongosh available and MONGODB_URI exported in environment
#   - .env sourced or MONGODB_URI set

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
source "${REPO_ROOT}/deployments/gke/gke.env"
source "${REPO_ROOT}/.env"

EVIDENCE_DIR="${REPO_ROOT}/deployments/chaos/evidence/step10"
mkdir -p "${EVIDENCE_DIR}"

API_URL="http://localhost:8080"
ROUTER_URL="http://localhost:8084"

now_ms() {
  python3 -c 'import time; print(int(time.time() * 1000))'
}


echo "============================================================"
echo "  Step 10 — RTO and Failover Validation"
echo "  Evidence: ${EVIDENCE_DIR}"
echo "============================================================"
echo ""

# ── Helper: check port-forward is live ──────────────────────────────────────
check_portforward() {
  local url=$1 name=$2
  if ! curl -sf --connect-timeout 3 "${url}/health" > /dev/null 2>&1; then
    echo "ERROR: ${name} port-forward not active at ${url}"
    echo "Run: kubectl port-forward -n ${NAMESPACE} svc/${name} $(echo ${url} | grep -oP ':\K[0-9]+'):$(echo ${url} | grep -oP ':\K[0-9]+') &"
    exit 1
  fi
}

echo ">> Verifying port-forwards..."
check_portforward "${API_URL}" "api"
check_portforward "${ROUTER_URL}" "router"
echo "   ✓ Both port-forwards active."

# ── Preflight: MongoDB ping ──────────────────────────────────────────────────
echo ""
echo ">> Preflight: MongoDB connectivity..."
mongosh "${MONGODB_URI}" --quiet --eval "db.adminCommand({ping:1}).ok" \
  > "${EVIDENCE_DIR}/preflight_mongo_ping.txt" 2>&1
PING=$(cat "${EVIDENCE_DIR}/preflight_mongo_ping.txt" | tr -d '[:space:]')
if [ "${PING}" != "1" ]; then
  echo "ERROR: MongoDB ping failed. Check Atlas Network Access."
  exit 1
fi
echo "   ✓ MongoDB ping: OK"

# ── Capture baseline ─────────────────────────────────────────────────────────
echo ""
echo ">> Capturing baseline..."
kubectl get pods -n "${NAMESPACE}" -o wide > "${EVIDENCE_DIR}/baseline_pods.txt"
kubectl get services -n "${NAMESPACE}" > "${EVIDENCE_DIR}/baseline_services.txt"

curl -sf "${API_URL}/health" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/baseline_api_health.json"
curl -sf "${API_URL}/api/summary" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/baseline_api_summary.json"
curl -sf "${ROUTER_URL}/route" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/baseline_routing.json"

PREFERRED_BEFORE=$(python3 -c "
import json, sys
d = json.load(open('${EVIDENCE_DIR}/baseline_routing.json'))
print(d['preferred_region'])
")
echo "   Preferred region before tests: ${PREFERRED_BEFORE}"
echo "   Baseline evidence captured."

# ════════════════════════════════════════════════════════════════════════════
# TEST A — API POD RECOVERY (RTO)
# ════════════════════════════════════════════════════════════════════════════
echo ""
echo ">> TEST A: API pod recovery (RTO ≤ 60s target)"
echo "   ─────────────────────────────────────────────"

# Get current API pod name
API_POD=$(kubectl get pod -n "${NAMESPACE}" -l app=api \
  -o jsonpath='{.items[0].metadata.name}')
echo "   Deleting pod: ${API_POD}"

# Record start time and delete pod
RTO_START=$(now_ms)
kubectl delete pod "${API_POD}" -n "${NAMESPACE}"

echo "   Pod deleted at $(date -u +%H:%M:%S). Waiting for replacement..."

# Wait for rollout with 90s timeout (we'll measure elapsed at pass/fail)
ROLLOUT_RESULT="PASS"
kubectl rollout status deployment/api \
  --namespace "${NAMESPACE}" \
  --timeout=90s 2>&1 | tee "${EVIDENCE_DIR}/api_rollout_log.txt" || ROLLOUT_RESULT="FAIL"

RTO_END=$(now_ms)
RTO_MS=$((RTO_END - RTO_START))
RTO_SECONDS=$(echo "scale=1; ${RTO_MS}/1000" | bc)

# Verify API is responding after recovery.
# On macOS, a kubectl port-forward to a Service can break when the selected backend pod is deleted.
# Restart the API port-forward after rollout, then test /health through the Service again.
pkill -f "kubectl port-forward -n ${NAMESPACE} svc/api 8080:8080" 2>/dev/null || true
sleep 1
kubectl port-forward -n "${NAMESPACE}" svc/api 8080:8080 > "${EVIDENCE_DIR}/api_portforward_after_rto.log" 2>&1 &
sleep 4

API_HEALTHY="no"
for i in {1..10}; do
  if curl -sf --connect-timeout 5 "${API_URL}/health" > "${EVIDENCE_DIR}/api_recovery_health.json" 2>&1; then
    API_HEALTHY="yes"
    break
  fi
  sleep 2
done

if [ "${API_HEALTHY}" != "yes" ]; then
  ROLLOUT_RESULT="FAIL"
fi

# Write RTO result
{
  echo "API Pod Recovery Test — Step 10"
  echo "Deleted pod: ${API_POD}"
  echo "Recovery time: ${RTO_SECONDS} seconds (${RTO_MS} ms)"
  echo "SLA target: ≤ 60 seconds"
  echo "API healthy after recovery: ${API_HEALTHY}"
  if [ "${ROLLOUT_RESULT}" = "PASS" ] && [ $(echo "${RTO_SECONDS} <= 60" | bc) -eq 1 ]; then
    echo "RESULT: PASS — API recovered within SLA target"
  elif [ "${ROLLOUT_RESULT}" = "PASS" ]; then
    echo "RESULT: SLOW — API recovered but exceeded 60s SLA (${RTO_SECONDS}s)"
  else
    echo "RESULT: FAIL — API did not recover within 90s window"
  fi
} > "${EVIDENCE_DIR}/rto_result.txt"

cat "${EVIDENCE_DIR}/rto_result.txt"

# ════════════════════════════════════════════════════════════════════════════
# TEST B — CONTROLLED REGION FAILOVER
# ════════════════════════════════════════════════════════════════════════════
echo ""
echo ">> TEST B: Controlled region failover"
echo "   ─────────────────────────────────────────────"

# Capture routing decision before failover test
curl -sf "${ROUTER_URL}/route" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/failover_before_routing.json"

PREFERRED=$(python3 -c "
import json
d = json.load(open('${EVIDENCE_DIR}/failover_before_routing.json'))
print(d['preferred_region'])
")
echo "   Current preferred region: ${PREFERRED}"
echo "   Marking ${PREFERRED} as down in Atlas..."

# Mark preferred region as down in region_health
mongosh "${MONGODB_URI}" --quiet << MONGO > "${EVIDENCE_DIR}/failover_region_patch.json"
use resiliency_serving
db.region_health.findOneAndUpdate(
  { region: "${PREFERRED}" },
  { \$set: { status: "down", latency_ms: 9999, replication_lag_ms: 9999,
             checked_at: new Date(), last_check: new Date() } },
  { returnDocument: "after" }
)
MONGO

echo "   Region ${PREFERRED} marked down. Triggering router refresh..."

# Trigger router refresh and capture result
curl -sf -X POST "${ROUTER_URL}/refresh" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/failover_refresh_response.json"

curl -sf "${ROUTER_URL}/route" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/failover_after_routing.json"

PREFERRED_AFTER=$(python3 -c "
import json
d = json.load(open('${EVIDENCE_DIR}/failover_after_routing.json'))
print(d['preferred_region'])
")
PREFERRED_STATUS=$(python3 -c "
import json
d = json.load(open('${EVIDENCE_DIR}/failover_after_routing.json'))
print(d['preferred_status'])
")
FALLBACK_AFTER=$(python3 -c "
import json
d = json.load(open('${EVIDENCE_DIR}/failover_after_routing.json'))
print(d['fallback_region'])
")
DOWN_COUNT=$(python3 -c "
import json
d = json.load(open('${EVIDENCE_DIR}/failover_after_routing.json'))
print(d['down_count'])
")

# Write failover result
{
  echo "Controlled Failover Test — Step 10"
  echo "Preferred region before test: ${PREFERRED}"
  echo "Preferred before: ${PREFERRED}"
  echo "Preferred after:  ${PREFERRED_AFTER}"
  echo "Preferred status after: ${PREFERRED_STATUS}"
  echo "Fallback after: ${FALLBACK_AFTER}"
  echo "Down count after: ${DOWN_COUNT}"
  if [ "${PREFERRED_AFTER}" != "${PREFERRED}" ] && [ "${PREFERRED_STATUS}" = "healthy" ]; then
    echo "PASS: router avoided the failed preferred region and selected a healthy alternative."
  else
    echo "FAIL: router did not change preferred region or selected an unhealthy region."
  fi
} > "${EVIDENCE_DIR}/failover_result.txt"

cat "${EVIDENCE_DIR}/failover_result.txt"

# ── Restore region ────────────────────────────────────────────────────────────
echo ""
echo ">> Restoring ${PREFERRED} to healthy..."
mongosh "${MONGODB_URI}" --quiet << MONGO > "${EVIDENCE_DIR}/failover_restore_response.json"
use resiliency_serving
db.region_health.findOneAndUpdate(
  { region: "${PREFERRED}" },
  { \$set: { status: "healthy", latency_ms: 29, replication_lag_ms: 58,
             checked_at: new Date(), last_check: new Date() } },
  { returnDocument: "after" }
)
MONGO

sleep 2
curl -sf -X POST "${ROUTER_URL}/refresh" > /dev/null
sleep 2

curl -sf "${ROUTER_URL}/route" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/final_routing.json"
curl -sf "${API_URL}/api/summary" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/final_api_summary.json"
curl -sf "${API_URL}/health" | python3 -m json.tool \
  > "${EVIDENCE_DIR}/final_api_health.json"

# ── Final GKE state capture ───────────────────────────────────────────────────
echo ""
echo ">> Capturing final GKE state..."
kubectl get pods -n "${NAMESPACE}" -o wide > "${EVIDENCE_DIR}/gke_final_pods.txt"
kubectl get services -n "${NAMESPACE}" > "${EVIDENCE_DIR}/gke_final_services.txt"
kubectl get deployments -n "${NAMESPACE}" > "${EVIDENCE_DIR}/gke_final_deployments.txt"
kubectl get nodes -o wide > "${EVIDENCE_DIR}/gke_nodes.txt"
kubectl get events -n "${NAMESPACE}" --sort-by='.lastTimestamp' | tail -40 \
  > "${EVIDENCE_DIR}/gke_final_events_tail.txt"

echo ""
cat "${EVIDENCE_DIR}/gke_final_pods.txt"

echo ""
echo "============================================================"
echo "  Step 10 Test Summary"
echo "  Evidence saved to: ${EVIDENCE_DIR}"
echo ""
echo "  TEST A (RTO):"
grep "RESULT:" "${EVIDENCE_DIR}/rto_result.txt"
echo ""
echo "  TEST B (Failover):"
grep "PASS\|FAIL" "${EVIDENCE_DIR}/failover_result.txt"
echo "============================================================"
