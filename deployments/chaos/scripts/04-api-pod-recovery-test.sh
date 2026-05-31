#!/usr/bin/env bash
set -euo pipefail

NS="${K8S_NAMESPACE:-resiliency}"
EVIDENCE_DIR="deployments/chaos/evidence"
mkdir -p "$EVIDENCE_DIR"

echo "== API pod recovery test =="

echo "Capturing API health before disruption..."
curl -s http://localhost:8080/health | tee "$EVIDENCE_DIR/api_recovery_before_health.json"
echo

API_POD="$(kubectl get pods -n "$NS" -l app=api -o jsonpath='{.items[0].metadata.name}')"

if [[ -z "$API_POD" ]]; then
  echo "No API pod found."
  exit 1
fi

echo "Deleting API pod: $API_POD" | tee "$EVIDENCE_DIR/api_recovery_result.txt"
START_TS="$(date +%s)"
kubectl delete pod "$API_POD" -n "$NS"

echo "Waiting for API deployment rollout..."
kubectl rollout status deployment/api -n "$NS" --timeout=180s | tee -a "$EVIDENCE_DIR/api_recovery_result.txt" || true

echo "Polling API health endpoint..."
for i in $(seq 1 60); do
  if curl -s http://localhost:8080/health | grep -q "healthy"; then
    END_TS="$(date +%s)"
    RTO=$((END_TS - START_TS))
    echo "PASS: API recovered in approximately ${RTO} seconds." | tee -a "$EVIDENCE_DIR/api_recovery_result.txt"
    curl -s http://localhost:8080/health | tee "$EVIDENCE_DIR/api_recovery_after_health.json"
    echo
    kubectl get pods -n "$NS" -l app=api -o wide | tee "$EVIDENCE_DIR/api_recovery_after_pods.txt"
    exit 0
  fi
  sleep 5
done

echo "CHECK NEEDED: API did not recover within 300 seconds." | tee -a "$EVIDENCE_DIR/api_recovery_result.txt"
kubectl get pods -n "$NS" -l app=api -o wide | tee "$EVIDENCE_DIR/api_recovery_after_pods.txt"
exit 1
