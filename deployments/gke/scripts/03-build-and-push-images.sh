#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

cd "${ROOT_DIR}"

echo "Building and pushing Linux/amd64 images to Artifact Registry..."
echo "Image base: ${IMAGE_BASE}"
echo "Tag: ${IMAGE_TAG}"

SERVICES=("ingestor" "transformer" "aggregator" "healthcheck" "router" "api")
for service in "${SERVICES[@]}"; do
  dockerfile="deployments/docker/Dockerfile.${service}"
  image="${IMAGE_BASE}/resiliency-${service}:${IMAGE_TAG}"
  echo
  echo "Building ${service}: ${image}"
  docker buildx build \
    --platform linux/amd64 \
    -f "${dockerfile}" \
    -t "${image}" \
    --push \
    .
done

echo
echo "Building dashboard image..."
docker buildx build \
  --platform linux/amd64 \
  -f deployments/docker/Dockerfile.dashboard \
  -t "${IMAGE_BASE}/resiliency-dashboard:${IMAGE_TAG}" \
  --push \
  .

echo "All images pushed."
