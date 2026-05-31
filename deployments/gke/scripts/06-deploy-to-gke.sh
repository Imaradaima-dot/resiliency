#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

cd "${ROOT_DIR}"

echo "Applying Kubernetes base manifests..."
kubectl apply -k deployments/k8s/base

echo "Updating deployment images to Artifact Registry..."
kubectl -n "${K8S_NAMESPACE}" set image deployment/ingestor ingestor="${IMAGE_BASE}/resiliency-ingestor:${IMAGE_TAG}"
kubectl -n "${K8S_NAMESPACE}" set image deployment/transformer transformer="${IMAGE_BASE}/resiliency-transformer:${IMAGE_TAG}"
kubectl -n "${K8S_NAMESPACE}" set image deployment/aggregator aggregator="${IMAGE_BASE}/resiliency-aggregator:${IMAGE_TAG}"
kubectl -n "${K8S_NAMESPACE}" set image deployment/healthcheck healthcheck="${IMAGE_BASE}/resiliency-healthcheck:${IMAGE_TAG}"
kubectl -n "${K8S_NAMESPACE}" set image deployment/router router="${IMAGE_BASE}/resiliency-router:${IMAGE_TAG}"
kubectl -n "${K8S_NAMESPACE}" set image deployment/api api="${IMAGE_BASE}/resiliency-api:${IMAGE_TAG}"
kubectl -n "${K8S_NAMESPACE}" set image deployment/dashboard dashboard="${IMAGE_BASE}/resiliency-dashboard:${IMAGE_TAG}"

echo "Waiting for deployments to roll out..."
DEPLOYMENTS=("ingestor" "transformer" "aggregator" "healthcheck" "router" "api" "dashboard" "prometheus" "grafana")
for d in "${DEPLOYMENTS[@]}"; do
  kubectl -n "${K8S_NAMESPACE}" rollout status deployment/"${d}" --timeout=180s || true
done

echo
kubectl get pods -n "${K8S_NAMESPACE}"
echo
kubectl get svc -n "${K8S_NAMESPACE}"
