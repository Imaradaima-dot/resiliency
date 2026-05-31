# Step 8: GKE Deployment

This step moves the Phase 3 implementation from Docker Compose/local Kubernetes into Google Kubernetes Engine (GKE).

## What this adds

- Artifact Registry setup for Docker images
- Multi-image build and push workflow
- GKE cluster creation commands
- Kubernetes secret creation from `.env`
- Deployment using the existing Step 7 Kubernetes manifests
- Port-forward commands for Streamlit, Grafana, and Prometheus
- Teardown commands to avoid ongoing charges

## Expected repo prerequisite

Run Step 8 from the root of the existing `resiliency` repo that already contains:

```text
deployments/k8s/base
deployments/k8s/overlays
deployments/docker
cmd/
internal/
dashboards/
docker-compose.yml
.env
```

## Required local tools

```bash
gcloud --version
kubectl version --client
docker --version
```

## Quick start

1. Copy the `deployments/gke` folder into your working repo.
2. Edit `deployments/gke/gke.env`.
3. Authenticate to Google Cloud.
4. Run the scripts in order.

```bash
cd /Users/graceburns/Documents/Data_Engineering/Data_Engineering_Group_project/implementation/resiliency

gcloud auth login
gcloud auth application-default login

cp deployments/gke/gke.env.example deployments/gke/gke.env
# edit deployments/gke/gke.env with your project id and region

./deployments/gke/scripts/00-check-prereqs.sh
./deployments/gke/scripts/01-enable-apis.sh
./deployments/gke/scripts/02-create-artifact-registry.sh
./deployments/gke/scripts/03-build-and-push-images.sh
./deployments/gke/scripts/04-create-gke-cluster.sh
./deployments/gke/scripts/05-create-k8s-secret.sh
./deployments/gke/scripts/06-deploy-to-gke.sh
```

## Validate

```bash
kubectl get pods -n resiliency
kubectl get svc -n resiliency
kubectl get deployments -n resiliency
```

## Open services locally through port-forwarding

Dashboard:

```bash
kubectl port-forward -n resiliency svc/dashboard 8501:8501
```

Open:

```text
http://localhost:8501
```

Grafana:

```bash
kubectl port-forward -n resiliency svc/grafana 3000:3000
```

Open:

```text
http://localhost:3000
```

Prometheus:

```bash
kubectl port-forward -n resiliency svc/prometheus 9090:9090
```

Open:

```text
http://localhost:9090/targets
```

## Cost control

When done, stop the cluster or delete it:

```bash
./deployments/gke/scripts/08-teardown-gke.sh
```

This project uses live cloud resources in Step 8. Do not leave the GKE cluster running unnecessarily.
