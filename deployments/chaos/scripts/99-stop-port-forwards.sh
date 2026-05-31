#!/usr/bin/env bash
set -euo pipefail

PID_DIR="deployments/chaos/.pids"

if [[ ! -d "$PID_DIR" ]]; then
  echo "No port-forward PID directory found."
  exit 0
fi

for pid_file in "$PID_DIR"/*.pid; do
  [[ -e "$pid_file" ]] || continue
  pid="$(cat "$pid_file")"
  name="$(basename "$pid_file" .pid)"
  if kill -0 "$pid" 2>/dev/null; then
    echo "Stopping $name port-forward with PID $pid"
    kill "$pid" || true
  else
    echo "$name port-forward is not running"
  fi
  rm -f "$pid_file"
done

echo "Port-forwards stopped."
