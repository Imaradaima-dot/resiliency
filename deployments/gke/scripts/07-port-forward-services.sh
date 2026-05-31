#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "${SCRIPT_DIR}/_common.sh"

cat <<EOF
Open three separate terminal windows if you want all three services at once.

Dashboard:
kubectl port-forward -n ${K8S_NAMESPACE} svc/dashboard 8501:8501
Open http://localhost:8501

Grafana:
kubectl port-forward -n ${K8S_NAMESPACE} svc/grafana 3000:3000
Open http://localhost:3000
Login: admin / resiliency

Prometheus:
kubectl port-forward -n ${K8S_NAMESPACE} svc/prometheus 9090:9090
Open http://localhost:9090/targets
EOF
