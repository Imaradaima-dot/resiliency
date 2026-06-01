#!/usr/bin/env bash
# scripts/step10_deploy_cluster_b.sh
#
# Deploys the second active GKE cluster (Cluster B) in us-west1.
# Cluster A (resiliency-gke, us-central1) must already be running.
#
# This script completes the two-cluster active-active architecture:
#
#   Cluster A: resiliency-gke    (us-central1-a)  ← already deployed
#   Cluster B: resiliency-gke-b  (us-west1-a)     ← this script
#
# Both clusters:
#   - Run the full 9-pod application stack
#   - Connect to the same MongoDB Atlas instance
#   - Each healthcheck writes its own region entry to region_health
#   - Each router reads ALL region_health entries and selects a route
#     based on latency as measured FROM THAT CLUSTER'S LOCATION
#
# Because Cluster A is in us-central1 and Cluster B is in us-west1,
# their Atlas ping latencies will differ — demonstrating that each
# region independently observes and responds to network conditions.
#
# Prerequisites:
#   - step10_deploy.sh already ran (Cluster A is Running)
#   - step10 images already pushed to Artifact Registry
#   - .env file at repo root with MONGODB_URI, GITHUB_TOKEN, OWM_API_KEY
#
# Usage:
#   ./scripts/step10_deploy_cluster_b.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

source "${REPO_ROOT}/deployments/gke/gke.env"
GKE_CLUSTER_B_NAME="${CLUSTER_B_NAME}"
GKE_CLUSTER_B_ZONE="${CLUSTER_B_ZONE}"
GKE_CLUSTER_B_REGION="${CLUSTER_B_REGION}"
source "${REPO_ROOT}/.env"
CLUSTER_B_NAME="${GKE_CLUSTER_B_NAME}"
CLUSTER_B_ZONE="${GKE_CLUSTER_B_ZONE}"
CLUSTER_B_REGION="${GKE_CLUSTER_B_REGION}"
REGION="${CLUSTER_B_REGION}"

K8S_DIR="${REPO_ROOT}/deployments/k8s"
SECRETS_TEMPLATE="${K8S_DIR}/01-secrets.yaml.template"

echo "============================================================"
echo "  Step 10 — Deploy Cluster B (second active region)"
echo "  Cluster : ${CLUSTER_B_NAME}"
echo "  Zone    : ${CLUSTER_B_ZONE}"
echo "  Region  : ${CLUSTER_B_REGION}"
echo "  Images  : :${IMAGE_TAG}"
echo "============================================================"
echo ""

# ── Step 1: Verify Cluster A is still running ────────────────────────────────
echo ">> Verifying Cluster A is running..."
CLUSTER_A_STATUS=$(gcloud container clusters describe "${CLUSTER_NAME}" \
  --project "${PROJECT_ID}" \
  --zone "${ZONE}" \
  --format "value(status)" 2>/dev/null || true)

if [ "${CLUSTER_A_STATUS}" != "RUNNING" ]; then
  echo "WARNING: Cluster A (${CLUSTER_NAME}) is not running."
  echo "         Deploy Cluster A first with: ./scripts/step10_deploy.sh"
  echo "         Continuing with Cluster B deployment anyway..."
else
  echo "   ✓ Cluster A (${CLUSTER_NAME}) is RUNNING."
fi

# ── Step 2: Create Cluster B ─────────────────────────────────────────────────
echo ""
CLUSTER_B_STATUS=$(gcloud container clusters describe "${CLUSTER_B_NAME}" \
  --project "${PROJECT_ID}" \
  --zone "${CLUSTER_B_ZONE}" \
  --format "value(status)" 2>/dev/null || true)

if [ "${CLUSTER_B_STATUS}" = "RUNNING" ]; then
  echo ">> Cluster B (${CLUSTER_B_NAME}) already exists — skipping creation."
else
  echo ">> Creating Cluster B: ${CLUSTER_B_NAME} in ${CLUSTER_B_ZONE}..."
  gcloud container clusters create "${CLUSTER_B_NAME}" \
    --project "${PROJECT_ID}" \
    --zone "${CLUSTER_B_ZONE}" \
    --num-nodes "${NUM_NODES}" \
    --machine-type "${MACHINE_TYPE}" \
    --enable-autoupgrade \
    --no-enable-basic-auth \
    --quiet
  echo "   ✓ Cluster B created."
fi

# ── Step 3: Get kubectl credentials for Cluster B ───────────────────────────
echo ""
echo ">> Fetching kubectl credentials for Cluster B..."
gcloud container clusters get-credentials "${CLUSTER_B_NAME}" \
  --zone "${CLUSTER_B_ZONE}" \
  --project "${PROJECT_ID}"

CONTEXT_B="gke_${PROJECT_ID}_${CLUSTER_B_ZONE}_${CLUSTER_B_NAME}"
echo "   ✓ kubectl context: ${CONTEXT_B}"

# ── Step 4: Get Cluster B node IPs for Atlas Network Access ─────────────────
echo ""
echo ">> Retrieving Cluster B node external IPs..."
NODE_IPS=$(kubectl get nodes \
  -o jsonpath='{.items[*].status.addresses[?(@.type=="ExternalIP")].address}')
echo "   Cluster B node IPs: ${NODE_IPS}"

echo ""
echo "================================================================"
echo "  ACTION REQUIRED: Update MongoDB Atlas Network Access"
echo "  Add CLUSTER B node IPs (in addition to Cluster A IPs):"
echo ""
for IP in ${NODE_IPS}; do
  echo "    ${IP}/32"
done
echo ""
echo "  Atlas UI: https://cloud.mongodb.com"
echo "  Network Access → Add IP Address"
echo "================================================================"
echo ""
read -p "Press ENTER after adding Cluster B IPs to Atlas Network Access... "

# ── Step 5: Apply namespace ──────────────────────────────────────────────────
echo ""
echo ">> Applying namespace..."
kubectl apply -f "${K8S_DIR}/00-namespace.yaml"

# ── Step 6: Generate Secret ──────────────────────────────────────────────────
echo ""
echo ">> Generating Kubernetes Secret..."
SECRETS_FILE=$(mktemp /tmp/secrets-XXXXXX.yaml)

MONGODB_URI_B64=$(echo -n "${MONGODB_URI}" | base64 | tr -d '\n')
GITHUB_TOKEN_B64=$(echo -n "${GITHUB_TOKEN:-}" | base64 | tr -d '\n')
OWM_API_KEY_B64=$(echo -n "${OWM_API_KEY:-}" | base64 | tr -d '\n')
GRAFANA_PASS_B64=$(echo -n "resiliency" | base64 | tr -d '\n')

sed \
  -e "s|__MONGODB_URI_B64__|${MONGODB_URI_B64}|g" \
  -e "s|__GITHUB_TOKEN_B64__|${GITHUB_TOKEN_B64}|g" \
  -e "s|__OWM_API_KEY_B64__|${OWM_API_KEY_B64}|g" \
  -e "s|__GRAFANA_ADMIN_PASSWORD_B64__|${GRAFANA_PASS_B64}|g" \
  "${SECRETS_TEMPLATE}" > "${SECRETS_FILE}"

kubectl apply -f "${SECRETS_FILE}"
rm -f "${SECRETS_FILE}"
echo "   ✓ Secret applied."

# ── Step 7: Apply ConfigMap + patch REGION to us-west1 ──────────────────────
echo ""
echo ">> Applying ConfigMap (REGION=us-east1 base, then patching to ${CLUSTER_B_REGION})..."
kubectl apply -f "${K8S_DIR}/02-configmap.yaml"

# Patch REGION to us-west1 for Cluster B so the healthcheck and router
# correctly identify which cluster they are running in
kubectl patch configmap resiliency-config \
  --namespace "${NAMESPACE}" \
  --type merge \
  --patch "{\"data\":{\"REGION\":\"${CLUSTER_B_REGION}\"}}"
echo "   ✓ REGION patched to ${CLUSTER_B_REGION}."

# ── Step 8: Apply remaining manifests ───────────────────────────────────────
echo ""
echo ">> Applying Prometheus config and all service manifests..."
kubectl apply -f "${K8S_DIR}/03-prometheus-config.yaml"
for MANIFEST in \
  "${K8S_DIR}/04-ingestor.yaml" \
  "${K8S_DIR}/05-transformer.yaml" \
  "${K8S_DIR}/06-aggregator.yaml" \
  "${K8S_DIR}/07-healthcheck.yaml" \
  "${K8S_DIR}/08-router.yaml" \
  "${K8S_DIR}/09-api.yaml" \
  "${K8S_DIR}/10-dashboard.yaml" \
  "${K8S_DIR}/11-prometheus.yaml" \
  "${K8S_DIR}/12-grafana.yaml" \
  "${K8S_DIR}/13-grafana-datasources.yaml" \
  "${K8S_DIR}/14-grafana-dashboards-config.yaml" \
  "${K8S_DIR}/15-grafana-dashboards-json.yaml"; do
  echo "   Applying $(basename ${MANIFEST})..."
  kubectl apply -f "${MANIFEST}"
done

# ── Step 9: Wait for all pods ────────────────────────────────────────────────
echo ""
echo ">> Waiting for all Cluster B pods to reach Running state (timeout 300s)..."
DEPLOYMENTS="ingestor transformer aggregator healthcheck router api dashboard prometheus grafana"
ALL_READY=true
for DEP in ${DEPLOYMENTS}; do
  echo -n "   Waiting for ${DEP}... "
  if kubectl rollout status deployment/"${DEP}" \
    --namespace "${NAMESPACE}" \
    --timeout=300s > /dev/null 2>&1; then
    echo "✓ Ready"
  else
    echo "✗ NOT Ready"
    ALL_READY=false
  fi
done

echo ""
echo ">> Cluster B pod status:"
kubectl get pods --namespace "${NAMESPACE}" -o wide

echo ""
echo ">> Cluster B external services:"
kubectl get svc grafana dashboard --namespace "${NAMESPACE}" 2>/dev/null || \
  kubectl get svc --namespace "${NAMESPACE}"

if [ "${ALL_READY}" = "false" ]; then
  echo ""
  echo "WARNING: Some pods not ready. Check:"
  echo "  kubectl describe pods -n ${NAMESPACE}"
  echo "  kubectl get events -n ${NAMESPACE} --sort-by='.lastTimestamp' | tail -20"
fi

echo ""
echo "============================================================"
echo "  Cluster B deployment complete."
echo ""
echo "  Both clusters are now active:"
echo "    Cluster A: ${CLUSTER_NAME} (${ZONE})"
echo "    Cluster B: ${CLUSTER_B_NAME} (${CLUSTER_B_ZONE})"
echo ""
echo "  Run the evidence capture script:"
echo "    ./scripts/step10_evidence_both_clusters.sh"
echo "============================================================"
