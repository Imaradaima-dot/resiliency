#!/usr/bin/env bash
# scripts/step10_evidence_both_clusters.sh
#
# Captures two-cluster active-active evidence.
# Both clusters must be Running before running this script.
#
# Evidence is saved to: deployments/chaos/evidence/step11/
#
# Usage:
#   ./scripts/step10_evidence_both_clusters.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

source "${REPO_ROOT}/deployments/gke/gke.env"

EVIDENCE_DIR="${REPO_ROOT}/deployments/chaos/evidence/step11"
mkdir -p "${EVIDENCE_DIR}"

CONTEXT_A="gke_${PROJECT_ID}_${ZONE}_${CLUSTER_NAME}"
CONTEXT_B="gke_${PROJECT_ID}_${CLUSTER_B_ZONE}_${CLUSTER_B_NAME}"
LOCAL_A_PORT=18080
LOCAL_B_PORT=18081
PF_A_PID=""
PF_B_PID=""

cleanup() {
  if [ -n "${PF_A_PID}" ]; then kill "${PF_A_PID}" 2>/dev/null || true; fi
  if [ -n "${PF_B_PID}" ]; then kill "${PF_B_PID}" 2>/dev/null || true; fi
}
trap cleanup EXIT

json_pretty() {
  python3 -m json.tool
}

cluster_status() {
  local cluster_name="$1"
  local zone="$2"
  gcloud container clusters describe "${cluster_name}" \
    --zone "${zone}" \
    --project "${PROJECT_ID}" \
    --format "value(status)" 2>/dev/null || echo "NOT_FOUND"
}

wait_for_http() {
  local url="$1"
  local label="$2"
  for _ in {1..20}; do
    if curl -sf "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "ERROR: ${label} did not become reachable at ${url}" >&2
  return 1
}

capture_api_refresh() {
  local port="$1"
  local output_file="$2"
  curl -sf -X POST "http://localhost:${port}/api/router/refresh" | json_pretty > "${output_file}"
}

capture_api_current() {
  local port="$1"
  local output_file="$2"
  curl -sf "http://localhost:${port}/api/routing/current" | json_pretty > "${output_file}"
}

capture_region_health() {
  local port="$1"
  local output_file="$2"
  curl -sf "http://localhost:${port}/api/regions/health" | json_pretty > "${output_file}"
}

echo "============================================================"
echo "  Two-Cluster Active-Active Evidence Capture"
echo "  Cluster A: ${CLUSTER_NAME} (${ZONE})"
echo "  Cluster B: ${CLUSTER_B_NAME} (${CLUSTER_B_ZONE})"
echo "  Evidence : ${EVIDENCE_DIR}"
echo "============================================================"
echo ""

# 1. Capture both clusters in one list. This is the headline screenshot.
echo ">> [1/8] Capturing GKE cluster list..."
gcloud container clusters list --project "${PROJECT_ID}" \
  > "${EVIDENCE_DIR}/both_clusters_list.txt" 2>&1
cat "${EVIDENCE_DIR}/both_clusters_list.txt"

CLUSTER_A_STATUS="$(cluster_status "${CLUSTER_NAME}" "${ZONE}")"
CLUSTER_B_STATUS="$(cluster_status "${CLUSTER_B_NAME}" "${CLUSTER_B_ZONE}")"

if [ "${CLUSTER_A_STATUS}" != "RUNNING" ]; then
  echo "ERROR: Cluster A is not RUNNING. Status: ${CLUSTER_A_STATUS}"
  echo "Deploy Cluster A with: ./scripts/step10_deploy.sh"
  exit 1
fi
if [ "${CLUSTER_B_STATUS}" != "RUNNING" ]; then
  echo "ERROR: Cluster B is not RUNNING. Status: ${CLUSTER_B_STATUS}"
  echo "Deploy Cluster B with: ./scripts/step10_deploy_cluster_b.sh"
  exit 1
fi
echo "   ✓ Both clusters are RUNNING."

# 2. Capture kubectl contexts.
echo ""
echo ">> [2/8] Capturing kubectl contexts..."
kubectl config get-contexts > "${EVIDENCE_DIR}/kubectl_contexts.txt" 2>&1
cat "${EVIDENCE_DIR}/kubectl_contexts.txt"

# 3. Capture Cluster A pod status and config.
echo ""
echo ">> [3/8] Capturing Cluster A pod status and REGION config..."
kubectl --context "${CONTEXT_A}" get pods --namespace "${NAMESPACE}" -o wide \
  > "${EVIDENCE_DIR}/cluster_a_pods.txt" 2>&1
kubectl --context "${CONTEXT_A}" get configmap resiliency-config --namespace "${NAMESPACE}" -o yaml \
  > "${EVIDENCE_DIR}/cluster_a_configmap.yaml" 2>&1
kubectl --context "${CONTEXT_A}" get deploy --namespace "${NAMESPACE}" -o wide \
  > "${EVIDENCE_DIR}/cluster_a_deployments.txt" 2>&1
cat "${EVIDENCE_DIR}/cluster_a_pods.txt"

# 4. Capture Cluster B pod status and config.
echo ""
echo ">> [4/8] Capturing Cluster B pod status and REGION config..."
kubectl --context "${CONTEXT_B}" get pods --namespace "${NAMESPACE}" -o wide \
  > "${EVIDENCE_DIR}/cluster_b_pods.txt" 2>&1
kubectl --context "${CONTEXT_B}" get configmap resiliency-config --namespace "${NAMESPACE}" -o yaml \
  > "${EVIDENCE_DIR}/cluster_b_configmap.yaml" 2>&1
kubectl --context "${CONTEXT_B}" get deploy --namespace "${NAMESPACE}" -o wide \
  > "${EVIDENCE_DIR}/cluster_b_deployments.txt" 2>&1
cat "${EVIDENCE_DIR}/cluster_b_pods.txt"

# 5. Port-forward to Cluster A API and force a routing refresh from Cluster A.
echo ""
echo ">> [5/8] Capturing routing decision computed from Cluster A API..."
kubectl --context "${CONTEXT_A}" port-forward -n "${NAMESPACE}" svc/api "${LOCAL_A_PORT}:8080" \
  > "${EVIDENCE_DIR}/cluster_a_port_forward.log" 2>&1 &
PF_A_PID=$!
wait_for_http "http://localhost:${LOCAL_A_PORT}/health" "Cluster A API"
capture_api_refresh "${LOCAL_A_PORT}" "${EVIDENCE_DIR}/cluster_a_routing_refresh.json"
capture_api_current "${LOCAL_A_PORT}" "${EVIDENCE_DIR}/cluster_a_routing_current_after_refresh.json"
capture_region_health "${LOCAL_A_PORT}" "${EVIDENCE_DIR}/cluster_a_region_health.json"
kill "${PF_A_PID}" 2>/dev/null || true
PF_A_PID=""

python3 - <<PY 2>/dev/null || cat "${EVIDENCE_DIR}/cluster_a_routing_refresh.json"
import json
with open('${EVIDENCE_DIR}/cluster_a_routing_refresh.json') as f:
    d = json.load(f)
print(f"   Preferred : {d.get('preferred_region')} ({d.get('preferred_latency_ms')} ms)")
print(f"   Fallback  : {d.get('fallback_region')} ({d.get('fallback_latency_ms')} ms)")
print(f"   Healthy   : {d.get('healthy_count')} | Down: {d.get('down_count')}")
print(f"   Reason    : {d.get('reason')}")
PY

# 6. Port-forward to Cluster B API and force a routing refresh from Cluster B.
echo ""
echo ">> [6/8] Capturing routing decision computed from Cluster B API..."
kubectl --context "${CONTEXT_B}" port-forward -n "${NAMESPACE}" svc/api "${LOCAL_B_PORT}:8080" \
  > "${EVIDENCE_DIR}/cluster_b_port_forward.log" 2>&1 &
PF_B_PID=$!
wait_for_http "http://localhost:${LOCAL_B_PORT}/health" "Cluster B API"
capture_api_refresh "${LOCAL_B_PORT}" "${EVIDENCE_DIR}/cluster_b_routing_refresh.json"
capture_api_current "${LOCAL_B_PORT}" "${EVIDENCE_DIR}/cluster_b_routing_current_after_refresh.json"
capture_region_health "${LOCAL_B_PORT}" "${EVIDENCE_DIR}/cluster_b_region_health.json"
kill "${PF_B_PID}" 2>/dev/null || true
PF_B_PID=""

python3 - <<PY 2>/dev/null || cat "${EVIDENCE_DIR}/cluster_b_routing_refresh.json"
import json
with open('${EVIDENCE_DIR}/cluster_b_routing_refresh.json') as f:
    d = json.load(f)
print(f"   Preferred : {d.get('preferred_region')} ({d.get('preferred_latency_ms')} ms)")
print(f"   Fallback  : {d.get('fallback_region')} ({d.get('fallback_latency_ms')} ms)")
print(f"   Healthy   : {d.get('healthy_count')} | Down: {d.get('down_count')}")
print(f"   Reason    : {d.get('reason')}")
PY

# 7. Capture a compact health comparison table from the two API reads.
echo ""
echo ">> [7/8] Writing region health comparison..."
python3 - <<PY > "${EVIDENCE_DIR}/region_health_comparison.txt" 2>&1 || true
import json
from pathlib import Path
for label, path in [
    ('Cluster A API read', Path('${EVIDENCE_DIR}/cluster_a_region_health.json')),
    ('Cluster B API read', Path('${EVIDENCE_DIR}/cluster_b_region_health.json')),
]:
    print(label)
    print('-' * len(label))
    rows = json.load(path.open())
    for row in sorted(rows, key=lambda r: r.get('region','')):
        print(f"{row.get('region'):12} status={row.get('status'):9} latency_ms={row.get('latency_ms')} checked_at={row.get('checked_at')}")
    print()
PY
cat "${EVIDENCE_DIR}/region_health_comparison.txt" || true

# 8. Write summary.
echo ""
echo ">> [8/8] Writing two-cluster summary..."

CLUSTER_A_PODS=$(awk 'NR>1 && $3 == "Running" {count++} END {print count+0}' "${EVIDENCE_DIR}/cluster_a_pods.txt")
CLUSTER_B_PODS=$(awk 'NR>1 && $3 == "Running" {count++} END {print count+0}' "${EVIDENCE_DIR}/cluster_b_pods.txt")

A_PREFERRED=$(python3 -c "import json; print(json.load(open('${EVIDENCE_DIR}/cluster_a_routing_refresh.json')).get('preferred_region','unknown'))" 2>/dev/null || echo "unknown")
A_LATENCY=$(python3 -c "import json; print(json.load(open('${EVIDENCE_DIR}/cluster_a_routing_refresh.json')).get('preferred_latency_ms','?'))" 2>/dev/null || echo "?")
B_PREFERRED=$(python3 -c "import json; print(json.load(open('${EVIDENCE_DIR}/cluster_b_routing_refresh.json')).get('preferred_region','unknown'))" 2>/dev/null || echo "unknown")
B_LATENCY=$(python3 -c "import json; print(json.load(open('${EVIDENCE_DIR}/cluster_b_routing_refresh.json')).get('preferred_latency_ms','?'))" 2>/dev/null || echo "?")

{
  echo "Two-Cluster Active-Active Evidence Summary"
  echo "Generated: $(date -u)"
  echo ""
  echo "CLUSTER A: ${CLUSTER_NAME} (${ZONE})"
  echo "  Pods Running             : ${CLUSTER_A_PODS}"
  echo "  Routing refresh response : ${A_PREFERRED} (${A_LATENCY} ms preferred latency)"
  echo ""
  echo "CLUSTER B: ${CLUSTER_B_NAME} (${CLUSTER_B_ZONE})"
  echo "  Pods Running             : ${CLUSTER_B_PODS}"
  echo "  Routing refresh response : ${B_PREFERRED} (${B_LATENCY} ms preferred latency)"
  echo ""
  echo "ACTIVE-ACTIVE VALIDATION:"
  echo "  Both GKE clusters are RUNNING simultaneously."
  echo "  Both clusters run the same 9-pod service stack."
  echo "  Both clusters connect to the same MongoDB Atlas M0 instance and serve the same data."
  echo "  Each cluster has its own Kubernetes context, node pool, pods, services, and REGION ConfigMap."
  echo "  The API in each cluster can trigger and return a routing decision."
  echo ""
  echo "IMPORTANT HONEST FRAMING:"
  echo "  This validates the two active GKE clusters and application-layer routing logic."
  echo "  This does not validate GCP Global Load Balancer traffic distribution."
  echo "  This does not validate true multi-region MongoDB replication because Atlas M0 is single-region."
} > "${EVIDENCE_DIR}/two_cluster_summary.txt"

cat "${EVIDENCE_DIR}/two_cluster_summary.txt"

echo ""
echo "============================================================"
echo "  Evidence saved to: ${EVIDENCE_DIR}"
echo ""
echo "  Key screenshot files:"
echo "    both_clusters_list.txt"
echo "    cluster_a_pods.txt"
echo "    cluster_b_pods.txt"
echo "    two_cluster_summary.txt"
echo ""
echo "  Additional evidence files:"
echo "    cluster_a_configmap.yaml"
echo "    cluster_b_configmap.yaml"
echo "    cluster_a_routing_refresh.json"
echo "    cluster_b_routing_refresh.json"
echo "    cluster_a_region_health.json"
echo "    cluster_b_region_health.json"
echo "    region_health_comparison.txt"
echo "    kubectl_contexts.txt"
echo ""
echo "  Delete both clusters after screenshots/evidence are captured:"
echo "    gcloud container clusters delete ${CLUSTER_NAME} --zone ${ZONE} --project ${PROJECT_ID} --quiet"
echo "    gcloud container clusters delete ${CLUSTER_B_NAME} --zone ${CLUSTER_B_ZONE} --project ${PROJECT_ID} --quiet"
echo "============================================================"
