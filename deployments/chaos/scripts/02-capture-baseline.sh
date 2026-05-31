#!/usr/bin/env bash
set -euo pipefail

NS="${K8S_NAMESPACE:-resiliency}"
EVIDENCE_DIR="deployments/chaos/evidence"
mkdir -p "$EVIDENCE_DIR"

echo "== Capturing baseline evidence =="

kubectl get pods -n "$NS" -o wide | tee "$EVIDENCE_DIR/baseline_pods.txt"
kubectl get svc -n "$NS" -o wide | tee "$EVIDENCE_DIR/baseline_services.txt"

curl -s http://localhost:8080/health | tee "$EVIDENCE_DIR/baseline_api_health.json"
echo
curl -s http://localhost:8080/api/summary | python3 -m json.tool | tee "$EVIDENCE_DIR/baseline_api_summary.json"
curl -s http://localhost:8080/api/routing/current | python3 -m json.tool | tee "$EVIDENCE_DIR/baseline_routing.json"
curl -s http://localhost:8084/route | python3 -m json.tool | tee "$EVIDENCE_DIR/baseline_router_route.json"

echo "Baseline capture complete."
