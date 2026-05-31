# Step 8 GKE Runbook

## Purpose

Deploy the Docker Compose validated implementation to Google Kubernetes Engine.

## Core migration concept

| Local Docker Compose | GKE |
|---|---|
| Docker images built locally | Images pushed to Artifact Registry |
| Compose services | Kubernetes Deployments |
| Compose service names | Kubernetes Services |
| `.env` file | Kubernetes Secret + ConfigMap |
| localhost ports | Port-forwarding or LoadBalancer |
| Docker Compose logs | `kubectl logs` |
| Docker Compose ps | `kubectl get pods` |

## Useful commands

### Show current context

```bash
kubectl config current-context
kubectl get nodes
```

### Watch pods

```bash
kubectl get pods -n resiliency -w
```

### Logs

```bash
kubectl logs -n resiliency deployment/api
kubectl logs -n resiliency deployment/dashboard
kubectl logs -n resiliency deployment/prometheus
```

### Restart deployment

```bash
kubectl rollout restart deployment/api -n resiliency
kubectl rollout restart deployment/dashboard -n resiliency
```

### Describe a failing pod

```bash
kubectl describe pod -n resiliency <pod-name>
```

### See recent events

```bash
kubectl get events -n resiliency --sort-by=.lastTimestamp
```

## Expected validation

After deployment:

```bash
kubectl get pods -n resiliency
```

All pods should eventually be `Running` or `Completed` depending on manifest type.

Prometheus targets should be available after port forwarding:

```bash
kubectl port-forward -n resiliency svc/prometheus 9090:9090
```

Open:

```text
http://localhost:9090/targets
```

Dashboard should open after:

```bash
kubectl port-forward -n resiliency svc/dashboard 8501:8501
```

Open:

```text
http://localhost:8501
```

## Known limitations

- This deployment uses a small demo GKE cluster, not a production-grade multi-zone cluster.
- The MongoDB Atlas connection remains external to the GKE cluster.
- The Streamlit dashboard is private by default and is exposed locally using port-forwarding.
- True multi-region failover validation requires either multiple regional GKE clusters or a controlled failure test.
