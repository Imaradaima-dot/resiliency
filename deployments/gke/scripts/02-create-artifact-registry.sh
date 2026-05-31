#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

gcloud config set project "${PROJECT_ID}"

echo "Configuring Docker authentication for ${AR_HOST}..."
gcloud auth configure-docker "${AR_HOST}" --quiet

echo "Creating Artifact Registry Docker repository if it does not exist..."
if gcloud artifacts repositories describe "${AR_REPO}" --location="${REGION}" >/dev/null 2>&1; then
  echo "Artifact Registry repository already exists: ${AR_REPO}"
else
  gcloud artifacts repositories create "${AR_REPO}" \
    --repository-format=docker \
    --location="${REGION}" \
    --description="Global service resiliency Docker images"
fi

echo "Artifact Registry ready: ${IMAGE_BASE}"
