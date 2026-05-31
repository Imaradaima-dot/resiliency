#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

echo "This will delete the Kubernetes namespace and optionally the GKE cluster."
read -r -p "Delete Kubernetes namespace ${K8S_NAMESPACE}? [y/N] " delete_ns
if [[ "${delete_ns}" =~ ^[Yy]$ ]]; then
  kubectl delete namespace "${K8S_NAMESPACE}" --ignore-not-found=true
fi

read -r -p "Delete GKE cluster ${CLUSTER_NAME} in ${ZONE}? This stops cluster charges. [y/N] " delete_cluster
if [[ "${delete_cluster}" =~ ^[Yy]$ ]]; then
  gcloud container clusters delete "${CLUSTER_NAME}" --zone "${ZONE}" --quiet
fi

read -r -p "Delete Artifact Registry repo ${AR_REPO} in ${REGION}? This deletes pushed images. [y/N] " delete_repo
if [[ "${delete_repo}" =~ ^[Yy]$ ]]; then
  gcloud artifacts repositories delete "${AR_REPO}" --location="${REGION}" --quiet
fi

echo "Teardown complete."
