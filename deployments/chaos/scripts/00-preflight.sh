#!/usr/bin/env bash
set -euo pipefail

NS="${K8S_NAMESPACE:-resiliency}"
EVIDENCE_DIR="deployments/chaos/evidence"
mkdir -p "$EVIDENCE_DIR"

echo "== Step 9 Preflight =="

echo "Checking kubectl..."
kubectl version --client >/dev/null
kubectl get namespace "$NS" >/dev/null

echo "Checking cluster pods..."
kubectl get pods -n "$NS" | tee "$EVIDENCE_DIR/preflight_pods.txt"

echo "Checking required local tools..."
command -v python3 >/dev/null || { echo "python3 is required"; exit 1; }
command -v curl >/dev/null || { echo "curl is required"; exit 1; }

if ! command -v mongosh >/dev/null; then
  echo "mongosh is not installed. Install with: brew install mongosh"
  exit 1
fi

echo "Checking Kubernetes secret..."
kubectl get secret resiliency-secrets -n "$NS" >/dev/null

echo "Checking MONGODB_URI availability..."
if [[ -f ".env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

if [[ -z "${MONGODB_URI:-}" ]]; then
  echo "MONGODB_URI not found in local .env. Trying Kubernetes secret."
  MONGODB_URI="$(kubectl get secret resiliency-secrets -n "$NS" -o jsonpath='{.data.MONGODB_URI}' | base64 --decode)"
fi

if [[ -z "${MONGODB_URI:-}" ]]; then
  echo "MONGODB_URI is still empty. Check .env or the resiliency-secrets secret."
  exit 1
fi

echo "Checking MongoDB Atlas connectivity..."
mongosh "$MONGODB_URI" --quiet --eval 'db.adminCommand({ping:1}).ok' | tee "$EVIDENCE_DIR/preflight_mongo_ping.txt"

echo "Preflight complete."
echo "Evidence written to $EVIDENCE_DIR"
