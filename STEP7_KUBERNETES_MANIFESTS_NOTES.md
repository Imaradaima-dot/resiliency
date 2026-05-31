# Phase 3 Step 7 - Kubernetes Manifests

This step adds Kubernetes manifests for the Phase 3 implementation stack. It does not replace Docker Compose immediately. Docker Compose remains the easiest local demo runtime. The Kubernetes manifests provide the cloud-native deployment path required by the Phase 2 design and prepare the project for GKE or another Kubernetes cluster.

## What was added

```text
deployments/k8s/
├── base/
│   ├── namespace.yaml
│   ├── configmap.yaml
│   ├── secret.example.yaml
│   ├── workloads.yaml
│   ├── observability.yaml
│   ├── prometheus.yml
│   ├── grafana-datasource-prometheus.yml
│   ├── grafana-dashboard-provider.yml
│   ├── grafana-dashboards/resiliency-observability.json
│   └── kustomization.yaml
├── overlays/
│   ├── us-east1/
│   ├── us-west1/
│   └── public/
└── scripts/
    ├── build-local-images.sh
    ├── build-and-push-artifact-registry.sh
    └── create-secret-from-env.sh
```

## Kubernetes services covered

The manifests deploy the full Step 6 stack:

```text
ingestor
transformer
aggregator
healthcheck
router
api
dashboard
prometheus
grafana
```

## Local Kubernetes run path

Use this path if you are running Docker Desktop Kubernetes or another local cluster that can access local Docker images.

### 1. Build the local images

From the project root:

```bash
./deployments/k8s/scripts/build-local-images.sh
```

### 2. Create the Kubernetes secret from `.env`

Make sure `.env` already contains valid values for:

```text
MONGODB_URI
GITHUB_TOKEN
OWM_API_KEY
```

Then run:

```bash
./deployments/k8s/scripts/create-secret-from-env.sh
```

### 3. Apply the base manifests

```bash
kubectl apply -k deployments/k8s/base
```

### 4. Watch pods start

```bash
kubectl get pods -n resiliency -w
```

### 5. Port-forward the dashboard

```bash
kubectl port-forward -n resiliency svc/dashboard 8501:8501
```

Open:

```text
http://localhost:8501
```

### 6. Port-forward Grafana

In another terminal:

```bash
kubectl port-forward -n resiliency svc/grafana 3000:3000
```

Open:

```text
http://localhost:3000
```

Login:

```text
username: admin
password: resiliency
```

### 7. Port-forward Prometheus

```bash
kubectl port-forward -n resiliency svc/prometheus 9090:9090
```

Open:

```text
http://localhost:9090/targets
```

All application services should show as `UP`.

## Multi-region deployment path

Two overlays are provided to support the multi-region design:

```text
deployments/k8s/overlays/us-east1
deployments/k8s/overlays/us-west1
```

These overlays patch the `REGION` value in the `resiliency-config` ConfigMap.

For a two-cluster GKE implementation, apply each overlay to the matching regional cluster context:

```bash
kubectl config use-context <us-east1-cluster-context>
./deployments/k8s/scripts/create-secret-from-env.sh
kubectl apply -k deployments/k8s/overlays/us-east1

kubectl config use-context <us-west1-cluster-context>
./deployments/k8s/scripts/create-secret-from-env.sh
kubectl apply -k deployments/k8s/overlays/us-west1
```

## Optional public exposure overlay

The base manifests use `ClusterIP` services and port-forwarding to avoid exposing dashboards publicly by accident.

For a temporary cloud demo, the `public` overlay changes the dashboard, Prometheus, and Grafana services to `LoadBalancer`:

```bash
kubectl apply -k deployments/k8s/overlays/public
kubectl get svc -n resiliency
```

Use this only when you intentionally want public external IPs. On GKE, `LoadBalancer` services can create cloud resources and may incur charges.

## GKE image push path

For GKE, images must be pushed to Artifact Registry or another container registry.

Example:

```bash
export PROJECT_ID="your-gcp-project"
export REGION="us-central1"
export REPOSITORY="resiliency"

gcloud auth configure-docker ${REGION}-docker.pkg.dev
./deployments/k8s/scripts/build-and-push-artifact-registry.sh
```

After pushing, update the image mappings in `deployments/k8s/base/kustomization.yaml` or create a GKE overlay with your registry image names.

## Validation checklist

```bash
kubectl get ns resiliency
kubectl get pods -n resiliency
kubectl get svc -n resiliency
kubectl logs -n resiliency deploy/api --tail=50
kubectl logs -n resiliency deploy/dashboard --tail=50
kubectl logs -n resiliency deploy/prometheus --tail=50
```

Expected service-level outcomes:

```text
Dashboard: http://localhost:8501 after port-forward
API health: kubectl port-forward svc/api 8080:8080, then curl http://localhost:8080/health
Prometheus targets: all services UP
Grafana dashboard: Global Service Resiliency Observability loads
```

## Clean up

```bash
kubectl delete -k deployments/k8s/base
kubectl delete namespace resiliency
```

If you used the `public` overlay, delete it first:

```bash
kubectl delete -k deployments/k8s/overlays/public
```

## Implementation caveats

- The Kubernetes manifests are deployment-ready templates, but they have not been validated against your specific GKE project yet.
- The base manifests use local image names such as `resiliency-api:latest`. For GKE, push images to a registry and update the Kustomize image mappings.
- The dashboard, Prometheus, and Grafana services are private by default. Use port-forwarding locally or the public overlay for a temporary cloud demo.
- `region_health.replication_lag_ms` remains an estimated implementation signal unless Atlas metrics or replica-set optime comparison is added later.
