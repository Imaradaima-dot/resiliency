#!/usr/bin/env bash
set -euo pipefail

NS="${K8S_NAMESPACE:-resiliency}"
EVIDENCE_DIR="deployments/chaos/evidence"
mkdir -p "$EVIDENCE_DIR"

echo "== Capturing final evidence =="

kubectl get pods -n "$NS" -o wide | tee "$EVIDENCE_DIR/final_pods.txt"
kubectl get deployments -n "$NS" -o wide | tee "$EVIDENCE_DIR/final_deployments.txt"
kubectl get svc -n "$NS" -o wide | tee "$EVIDENCE_DIR/final_services.txt"
kubectl get events -n "$NS" --sort-by=.lastTimestamp | tail -80 | tee "$EVIDENCE_DIR/final_events_tail.txt"

curl -s http://localhost:8080/api/summary | python3 -m json.tool | tee "$EVIDENCE_DIR/final_api_summary.json"
curl -s http://localhost:8080/api/routing/current | python3 -m json.tool | tee "$EVIDENCE_DIR/final_routing.json"

echo "Final evidence capture complete."
echo "Evidence directory: $EVIDENCE_DIR"
ls -lh "$EVIDENCE_DIR"
