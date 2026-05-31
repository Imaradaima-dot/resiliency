#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
ENV_FILE="${ROOT_DIR}/deployments/gke/gke.env"

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "Missing ${ENV_FILE}"
  echo "Copy deployments/gke/gke.env.example to deployments/gke/gke.env and edit it."
  exit 1
fi

# shellcheck disable=SC1090
source "${ENV_FILE}"

AR_HOST="${REGION}-docker.pkg.dev"
IMAGE_BASE="${AR_HOST}/${PROJECT_ID}/${AR_REPO}"
