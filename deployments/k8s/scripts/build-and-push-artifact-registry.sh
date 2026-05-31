#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   export PROJECT_ID="your-gcp-project"
#   export REGION="us-central1"
#   export REPOSITORY="resiliency"
#   ./deployments/k8s/scripts/build-and-push-artifact-registry.sh
#
# Prerequisites:
#   gcloud auth configure-docker ${REGION}-docker.pkg.dev
#   gcloud artifacts repositories create ${REPOSITORY} --repository-format=docker --location=${REGION}

: "${PROJECT_ID:?PROJECT_ID is required}"
: "${REGION:=us-central1}"
: "${REPOSITORY:=resiliency}"

BASE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPOSITORY}"

build_push () {
  local service="$1"
  local dockerfile="$2"
  local image="${BASE}/${service}:latest"
  docker build -f "${dockerfile}" -t "${image}" .
  docker push "${image}"
}

build_push ingestor deployments/docker/Dockerfile.ingestor
build_push transformer deployments/docker/Dockerfile.transformer
build_push aggregator deployments/docker/Dockerfile.aggregator
build_push healthcheck deployments/docker/Dockerfile.healthcheck
build_push router deployments/docker/Dockerfile.router
build_push api deployments/docker/Dockerfile.api
build_push dashboard deployments/docker/Dockerfile.dashboard

cat <<MSG

Images pushed to ${BASE}.
Next, edit deployments/k8s/base/kustomization.yaml or create a GKE overlay that maps:
  resiliency-ingestor    -> ${BASE}/ingestor:latest
  resiliency-transformer -> ${BASE}/transformer:latest
  resiliency-aggregator  -> ${BASE}/aggregator:latest
  resiliency-healthcheck -> ${BASE}/healthcheck:latest
  resiliency-router      -> ${BASE}/router:latest
  resiliency-api         -> ${BASE}/api:latest
  resiliency-dashboard   -> ${BASE}/dashboard:latest
MSG
