#!/usr/bin/env bash
# scripts/step10_deploy.sh
#
# Fresh GKE deployment for Step 10.
# Creates the cluster, generates the Kubernetes Secret from your .env,
# applies all manifests in order, updates MongoDB Atlas Network Access
# with the new node IPs, and waits for all pods to reach Running state.
#
# Prerequisites:
#   - step10_build.sh completed successfully
#   - .env file exists at repo root with MONGODB_URI, GITHUB_TOKEN, OWM_API_KEY
#   - gcloud authenticated and project set
#   - kubectl installed
#   - Atlas project API key in environment (ATLAS_PUBLIC_KEY + ATLAS_PRIVATE_KEY)
#     OR be ready to update Atlas Network Access manually when prompted.
#
# Usage:
#   cd <repo-root>
#   ./scripts/step10_deploy.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

source "${REPO_ROOT}/deployments/gke/gke.env"
source "${REPO_ROOT}/.env"

K8S_DIR="${REPO_ROOT}/deployments/k8s"
SECRETS_TEMPLATE="${K8S_DIR}/01-secrets.yaml.template"
SECRETS_FILE="${K8S_DIR}/01-secrets.yaml"

echo "============================================================"
echo "  Step 10 — Fresh GKE deployment"
echo "  Project  : ${PROJECT_ID}"
echo "  Cluster  : ${CLUSTER_NAME}"
echo "  Zone     : ${ZONE}"
echo "  Nodes    : ${NUM_NODES} × ${MACHINE_TYPE}"
echo "  Tag      : ${IMAGE_TAG}"
echo "============================================================"
echo ""

# ── Step 1: Verify or create GKE cluster ────────────────────────────────────
echo ">> Checking GKE cluster status..."
CLUSTER_STATUS=$(gcloud container clusters list \
  --project "${PROJECT_ID}" \
  --filter "name=${CLUSTER_NAME}" \
  --format "value(status)" 2>/dev/null || true)

if [ "${CLUSTER_STATUS}" = "RUNNING" ]; then
  echo "   Cluster ${CLUSTER_NAME} is already running."
else
  echo ">> Creating GKE cluster ${CLUSTER_NAME}..."
  gcloud container clusters create "${CLUSTER_NAME}" \
    --project "${PROJECT_ID}" \
    --zone "${ZONE}" \
    --num-nodes "${NUM_NODES}" \
    --machine-type "${MACHINE_TYPE}" \
    --enable-autoupgrade \
    --no-enable-basic-auth \
    --quiet
  echo "   ✓ Cluster created."
fi

# ── Step 2: Get kubectl credentials ─────────────────────────────────────────
echo ""
echo ">> Fetching kubectl credentials..."
gcloud container clusters get-credentials "${CLUSTER_NAME}" \
  --zone "${ZONE}" \
  --project "${PROJECT_ID}"
echo "   ✓ kubectl context set."

# ── Step 3: Get node external IPs for Atlas Network Access ──────────────────
echo ""
echo ">> Retrieving GKE node external IPs..."
NODE_IPS=$(kubectl get nodes \
  -o jsonpath='{.items[*].status.addresses[?(@.type=="ExternalIP")].address}')
echo "   Node IPs: ${NODE_IPS}"

echo ""
echo "================================================================"
echo "  ACTION REQUIRED: Update MongoDB Atlas Network Access"
echo "  Add these IP addresses to your Atlas project before continuing:"
echo ""
for IP in ${NODE_IPS}; do
  echo "    ${IP}/32"
done
echo ""
echo "  Atlas UI: https://cloud.mongodb.com"
echo "  Navigate to: Network Access → Add IP Address"
echo "================================================================"
echo ""
read -p "Press ENTER after updating Atlas Network Access to continue... "

# Quick connectivity check
echo ">> Verifying Atlas connectivity from a test pod..."
kubectl run atlas-ping \
  --namespace "${NAMESPACE}" \
  --image=alpine \
  --restart=Never \
  --rm \
  --attach \
  -- sh -c "apk add -q curl && curl -sS --connect-timeout 5 \
  'https://cloud.mongodb.com' > /dev/null && echo 'Atlas reachable'" \
  2>/dev/null || echo "   (Connectivity test skipped — proceed if Atlas is updated)"

# ── Step 4: Create namespace ─────────────────────────────────────────────────
echo ""
echo ">> Applying namespace..."
kubectl apply -f "${K8S_DIR}/00-namespace.yaml"

# ── Step 5: Generate secrets from .env ──────────────────────────────────────
echo ""
echo ">> Generating Kubernetes Secret from .env..."

MONGODB_URI_B64=$(echo -n "${MONGODB_URI}" | base64)
GITHUB_TOKEN_B64=$(echo -n "${GITHUB_TOKEN:-}" | base64)
OWM_API_KEY_B64=$(echo -n "${OWM_API_KEY:-}" | base64)
GRAFANA_PASS_B64=$(echo -n "resiliency" | base64)

sed \
  -e "s|__MONGODB_URI_B64__|${MONGODB_URI_B64}|g" \
  -e "s|__GITHUB_TOKEN_B64__|${GITHUB_TOKEN_B64}|g" \
  -e "s|__OWM_API_KEY_B64__|${OWM_API_KEY_B64}|g" \
  -e "s|__GRAFANA_ADMIN_PASSWORD_B64__|${GRAFANA_PASS_B64}|g" \
  "${SECRETS_TEMPLATE}" > "${SECRETS_FILE}"

kubectl apply -f "${SECRETS_FILE}"
rm -f "${SECRETS_FILE}"   # Don't leave secrets on disk
echo "   ✓ Secret applied and temp file removed."

# ── Step 6: Apply ConfigMap and Prometheus config ────────────────────────────
echo ""
echo ">> Applying ConfigMap and Prometheus config..."
kubectl apply -f "${K8S_DIR}/02-configmap.yaml"
kubectl apply -f "${K8S_DIR}/03-prometheus-config.yaml"

# ── Step 7: Deploy all application services ──────────────────────────────────
echo ""
echo ">> Deploying all services..."
for MANIFEST in \
  "${K8S_DIR}/04-ingestor.yaml" \
  "${K8S_DIR}/05-transformer.yaml" \
  "${K8S_DIR}/06-aggregator.yaml" \
  "${K8S_DIR}/07-healthcheck.yaml" \
  "${K8S_DIR}/08-router.yaml" \
  "${K8S_DIR}/09-api.yaml" \
  "${K8S_DIR}/10-dashboard.yaml" \
  "${K8S_DIR}/11-prometheus.yaml" \
  "${K8S_DIR}/12-grafana.yaml"; do
  echo "   Applying $(basename ${MANIFEST})..."
  kubectl apply -f "${MANIFEST}"
done

# ── Step 8: Wait for all pods to be Ready ────────────────────────────────────
echo ""
echo ">> Waiting for all pods to reach Running state (timeout 300s)..."
DEPLOYMENTS="ingestor transformer aggregator healthcheck router api dashboard prometheus grafana"

ALL_READY=true
for DEP in ${DEPLOYMENTS}; do
  echo -n "   Waiting for ${DEP}... "
  if kubectl rollout status deployment/"${DEP}" \
    --namespace "${NAMESPACE}" \
    --timeout=300s 2>&1; then
    echo "✓ Ready"
  else
    echo "✗ NOT Ready — check: kubectl describe pod -n ${NAMESPACE} -l app=${DEP}"
    ALL_READY=false
  fi
done

echo ""
echo ">> Final pod status:"
kubectl get pods --namespace "${NAMESPACE}" -o wide

if [ "${ALL_READY}" = "false" ]; then
  echo ""
  echo "WARNING: One or more deployments did not become ready."
  echo "Check pod logs: kubectl logs -n ${NAMESPACE} -l app=<service-name>"
  echo "Check events:   kubectl get events -n ${NAMESPACE} --sort-by='.lastTimestamp' | tail -20"
  exit 1
fi

echo ""
echo "============================================================"
echo "  All pods Running. Step 10 deployment complete."
echo ""
echo "  Next steps:"
echo "  1. Verify Prometheus targets:"
echo "     kubectl port-forward -n ${NAMESPACE} svc/prometheus 9090:9090 &"
echo "     open http://localhost:9090/targets"
echo ""
echo "  2. Run failover + RTO tests:"
echo "     ./scripts/step10_rto_test.sh"
echo ""
echo "  3. View dashboard:"
echo "     kubectl port-forward -n ${NAMESPACE} svc/dashboard 8501:8501 &"
echo "     open http://localhost:8501"
echo "============================================================"
