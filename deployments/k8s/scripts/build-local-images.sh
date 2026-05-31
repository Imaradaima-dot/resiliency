#!/usr/bin/env bash
set -euo pipefail

# Build images with the same names referenced by the local Kubernetes manifests.
# This is intended for Docker Desktop Kubernetes or minikube using the local Docker daemon.

docker build -f deployments/docker/Dockerfile.ingestor -t resiliency-ingestor:latest .
docker build -f deployments/docker/Dockerfile.transformer -t resiliency-transformer:latest .
docker build -f deployments/docker/Dockerfile.aggregator -t resiliency-aggregator:latest .
docker build -f deployments/docker/Dockerfile.healthcheck -t resiliency-healthcheck:latest .
docker build -f deployments/docker/Dockerfile.router -t resiliency-router:latest .
docker build -f deployments/docker/Dockerfile.api -t resiliency-api:latest .
docker build -f deployments/docker/Dockerfile.dashboard -t resiliency-dashboard:latest .
