#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

gcloud config set project "${PROJECT_ID}"

echo "Creating GKE cluster if it does not exist..."
if gcloud container clusters describe "${CLUSTER_NAME}" --zone "${ZONE}" >/dev/null 2>&1; then
  echo "Cluster already exists: ${CLUSTER_NAME}"
else
  gcloud container clusters create "${CLUSTER_NAME}" \
    --zone "${ZONE}" \
    --num-nodes "${NUM_NODES}" \
    --machine-type "${MACHINE_TYPE}" \
    --enable-ip-alias \
    --workload-pool="${PROJECT_ID}.svc.id.goog"
fi

echo "Fetching cluster credentials..."
gcloud container clusters get-credentials "${CLUSTER_NAME}" --zone "${ZONE}" --project "${PROJECT_ID}"

kubectl get nodes
