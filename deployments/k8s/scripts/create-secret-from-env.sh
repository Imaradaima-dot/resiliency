#!/usr/bin/env bash
set -euo pipefail

# Loads .env if present and creates/updates the Kubernetes Secret.
# Real secret values are never written to source files.

if [[ -f .env ]]; then
  set -a
  source .env
  set +a
fi

: "${MONGODB_URI:?MONGODB_URI is required}"
: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${OWM_API_KEY:?OWM_API_KEY is required}"
: "${GRAFANA_ADMIN_PASSWORD:=resiliency}"

kubectl create namespace resiliency --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic resiliency-secrets \
  -n resiliency \
  --from-literal=MONGODB_URI="${MONGODB_URI}" \
  --from-literal=GITHUB_TOKEN="${GITHUB_TOKEN}" \
  --from-literal=OWM_API_KEY="${OWM_API_KEY}" \
  --from-literal=GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD}" \
  --dry-run=client -o yaml | kubectl apply -f -
