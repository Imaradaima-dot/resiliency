#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

echo "Project root: ${ROOT_DIR}"
echo "Project: ${PROJECT_ID}"
echo "Region: ${REGION}"
echo "Zone: ${ZONE}"
echo "Cluster: ${CLUSTER_NAME}"
echo "Artifact Registry repo: ${AR_REPO}"
echo

command -v gcloud >/dev/null || { echo "gcloud is not installed"; exit 1; }
command -v kubectl >/dev/null || { echo "kubectl is not installed"; exit 1; }
command -v docker >/dev/null || { echo "docker is not installed"; exit 1; }

gcloud config set project "${PROJECT_ID}"

echo "Checking auth..."
gcloud auth list

echo "Checking Docker..."
docker info >/dev/null

echo "Checking required repo paths..."
test -d "${ROOT_DIR}/cmd/ingestor"
test -d "${ROOT_DIR}/cmd/transformer"
test -d "${ROOT_DIR}/cmd/aggregator"
test -d "${ROOT_DIR}/cmd/healthcheck"
test -d "${ROOT_DIR}/cmd/router"
test -d "${ROOT_DIR}/cmd/api"
test -d "${ROOT_DIR}/dashboards/streamlit"
test -d "${ROOT_DIR}/deployments/k8s/base"

echo "Prerequisite check complete."
