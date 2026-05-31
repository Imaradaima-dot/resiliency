#!/usr/bin/env bash
set -euo pipefail

NS="${K8S_NAMESPACE:-resiliency}"
PID_DIR="deployments/chaos/.pids"
mkdir -p "$PID_DIR"

start_pf() {
  local name="$1"
  local svc="$2"
  local local_port="$3"
  local remote_port="$4"
  local pid_file="$PID_DIR/${name}.pid"

  if [[ -f "$pid_file" ]] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
    echo "$name port-forward already running with PID $(cat "$pid_file")"
    return
  fi

  echo "Starting port-forward for $name on localhost:${local_port}..."
  kubectl port-forward -n "$NS" "svc/${svc}" "${local_port}:${remote_port}" > "$PID_DIR/${name}.log" 2>&1 &
  echo $! > "$pid_file"
  sleep 2
}

start_pf "api" "api" "8080" "8080"
start_pf "router" "router" "8084" "8084"
start_pf "dashboard" "dashboard" "8501" "8501"
start_pf "prometheus" "prometheus" "9090" "9090"

echo "Port-forwards started."
echo "API:        http://localhost:8080/health"
echo "Router:     http://localhost:8084/route"
echo "Dashboard:  http://localhost:8501"
echo "Prometheus: http://localhost:9090/targets"
