#!/usr/bin/env bash
# scripts/step10_build.sh
#
# Builds and pushes fresh step10 Docker images to Google Artifact Registry.
# Run this BEFORE step10_deploy.sh.
#
# Prerequisites:
#   - Docker running locally
#   - gcloud authenticated: gcloud auth login && gcloud auth configure-docker us-central1-docker.pkg.dev
#   - From repo root: ./scripts/step10_build.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

source "${REPO_ROOT}/deployments/gke/gke.env"

echo "============================================================"
echo "  Step 10 — Build and push images"
echo "  Registry : ${REGISTRY}"
echo "  Tag      : ${IMAGE_TAG}"
echo "============================================================"
echo ""

# Confirm Docker is running
if ! docker info > /dev/null 2>&1; then
  echo "ERROR: Docker is not running. Start Docker Desktop and retry."
  exit 1
fi

# Configure Docker auth for Artifact Registry
echo ">> Configuring Docker for Artifact Registry..."
gcloud auth configure-docker us-central1-docker.pkg.dev --quiet

cd "${REPO_ROOT}"

# Build and push each service
for SVC in ${SERVICES}; do
  IMAGE="${REGISTRY}/resiliency-${SVC}:${IMAGE_TAG}"
  DOCKERFILE="deployments/docker/Dockerfile.${SVC}"

  if [ ! -f "${DOCKERFILE}" ]; then
    echo "WARN: ${DOCKERFILE} not found — skipping ${SVC}"
    continue
  fi

  echo ""
  echo ">> Building ${SVC} → ${IMAGE}"
  docker build \
    --platform linux/amd64 \
    -f "${DOCKERFILE}" \
    -t "${IMAGE}" \
    .

  echo ">> Pushing ${SVC}..."
  docker push "${IMAGE}"
  echo "   ✓ ${SVC} pushed"
done

echo ""
echo "============================================================"
echo "  All images pushed with tag: ${IMAGE_TAG}"
echo "  Next step: ./scripts/step10_deploy.sh"
echo "============================================================"
