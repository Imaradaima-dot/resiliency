#!/usr/bin/env bash
set -euo pipefail

NS="${K8S_NAMESPACE:-resiliency}"
EVIDENCE_DIR="deployments/chaos/evidence"
mkdir -p "$EVIDENCE_DIR"

if [[ -f ".env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

if [[ -z "${MONGODB_URI:-}" ]]; then
  MONGODB_URI="$(kubectl get secret resiliency-secrets -n "$NS" -o jsonpath='{.data.MONGODB_URI}' | base64 --decode)"
fi

echo "== Simulating preferred region failure =="

echo "Capturing routing before failover..."
curl -s http://localhost:8080/api/routing/current | python3 -m json.tool | tee "$EVIDENCE_DIR/failover_before_routing.json"

PREFERRED_REGION="$(python3 - <<'PY'
import json
from pathlib import Path
data = json.loads(Path("deployments/chaos/evidence/failover_before_routing.json").read_text())
print(data.get("preferred_region", ""))
PY
)"

if [[ -z "$PREFERRED_REGION" || "$PREFERRED_REGION" == "N/A" ]]; then
  echo "Could not determine preferred region. Aborting."
  exit 1
fi

echo "Preferred region before test: $PREFERRED_REGION" | tee "$EVIDENCE_DIR/failover_result.txt"

echo "Scaling healthcheck to 0 so it does not overwrite the test condition..."
kubectl scale deployment/healthcheck -n "$NS" --replicas=0
kubectl rollout status deployment/healthcheck -n "$NS" --timeout=60s || true

echo "Marking $PREFERRED_REGION as down in resiliency_serving.region_health..."
mongosh "$MONGODB_URI" --quiet --eval "
const dbs = db.getSiblingDB('resiliency_serving');
dbs.region_health.updateOne(
  { region: '$PREFERRED_REGION' },
  { \$set: {
      status: 'down',
      latency_ms: 9999,
      replication_lag_ms: 9999,
      last_check: new Date(),
      checked_at: new Date(),
      read_preference: 'nearest',
      write_concern: 'w:1 health heartbeat; majority used for raw/processed writes'
    }
  },
  { upsert: true }
);
printjson(dbs.region_health.find({region: '$PREFERRED_REGION'}).toArray());
" | tee "$EVIDENCE_DIR/failover_region_patch.json"

echo "Refreshing router decision..."
curl -s -X POST http://localhost:8084/route/refresh | python3 -m json.tool | tee "$EVIDENCE_DIR/failover_refresh_response.json"
sleep 3
curl -s http://localhost:8080/api/routing/current | python3 -m json.tool | tee "$EVIDENCE_DIR/failover_after_routing.json"

python3 - <<'PY' | tee -a "deployments/chaos/evidence/failover_result.txt"
import json
from pathlib import Path

before = json.loads(Path("deployments/chaos/evidence/failover_before_routing.json").read_text())
after = json.loads(Path("deployments/chaos/evidence/failover_after_routing.json").read_text())

old_region = before.get("preferred_region")
new_region = after.get("preferred_region")
new_status = after.get("preferred_status")
fallback_region = after.get("fallback_region")

print(f"Preferred before: {old_region}")
print(f"Preferred after:  {new_region}")
print(f"Preferred status after: {new_status}")
print(f"Fallback after: {fallback_region}")

if new_region and new_region != old_region and new_status == "healthy":
    print("PASS: router avoided the failed preferred region and selected a healthy alternative.")
else:
    print("CHECK NEEDED: router did not move away from the failed region as expected.")
PY

echo "Restoring healthcheck to 1 replica..."
kubectl scale deployment/healthcheck -n "$NS" --replicas=1
kubectl rollout status deployment/healthcheck -n "$NS" --timeout=120s || true

echo "Waiting for healthcheck to refresh region health..."
sleep 20
curl -s -X POST http://localhost:8084/route/refresh | python3 -m json.tool | tee "$EVIDENCE_DIR/failover_restore_refresh_response.json"
curl -s http://localhost:8080/api/routing/current | python3 -m json.tool | tee "$EVIDENCE_DIR/failover_restored_routing.json"

echo "Failover simulation complete."
