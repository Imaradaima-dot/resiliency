#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

cd "${ROOT_DIR}"

if [[ ! -f ".env" ]]; then
  echo "Missing .env in project root. This script reads MONGODB_URI, GITHUB_TOKEN, and OWM_API_KEY from .env."
  exit 1
fi

kubectl create namespace "${K8S_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

set -a
# shellcheck disable=SC1091
source .env
set +a

: "${MONGODB_URI:?MONGODB_URI missing from .env}"
: "${OWM_API_KEY:?OWM_API_KEY missing from .env}"

# GITHUB_TOKEN may be blank if using unauthenticated API, but authenticated is recommended.
GITHUB_TOKEN="${GITHUB_TOKEN:-}"

kubectl -n "${K8S_NAMESPACE}" create secret generic resiliency-secrets \
  --from-literal=MONGODB_URI="${MONGODB_URI}" \
  --from-literal=GITHUB_TOKEN="${GITHUB_TOKEN}" \
  --from-literal=OWM_API_KEY="${OWM_API_KEY}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Secret resiliency-secrets created/updated in namespace ${K8S_NAMESPACE}."
