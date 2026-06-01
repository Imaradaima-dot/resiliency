# GitHub Secrets Setup Guide

Go to your repo → **Settings → Secrets and variables → Actions → New repository secret**
and create each secret below before the first pipeline run.

---

## Required Secrets

| Secret name | Where to get it | Example value |
|---|---|---|
| `GCP_SA_KEY` | GCP Console → IAM → Service Accounts | `{ "type": "service_account", ... }` (base64) |
| `GKE_CLUSTER_NAME` | GCP Console → Kubernetes Engine | `resiliency-cluster` |
| `GKE_CLUSTER_ZONE` | GCP Console → Kubernetes Engine | `us-east1-b` |
| `MONGO_URI` | MongoDB Atlas → Connect → Drivers | `mongodb+srv://replace_with_mongodb_atlas_connection_string |
| `GITHUB_TOKEN_SECRET` | GitHub → Settings → Developer settings → PAT | `replace_with_github_token` |
| `OWM_API_KEY` | openweathermap.org → API keys | `abc123def456...` |

> **Note:** The built-in `GITHUB_TOKEN` is reserved by GitHub Actions.
> Name your GitHub PAT `GITHUB_TOKEN_SECRET` to avoid the conflict.

---

## Creating the GCP Service Account

The service account needs these roles to build, push, and deploy:

```bash
# 1. Create the service account
gcloud iam service-accounts create github-actions-sa \
  --display-name="GitHub Actions CI/CD" \
  --project=msds-432-g2-497302

# 2. Grant required roles
SA="github-actions-sa@msds-432-g2-497302.iam.gserviceaccount.com"

gcloud projects add-iam-policy-binding msds-432-g2-497302 \
  --member="serviceAccount:${SA}" \
  --role="roles/container.developer"          # deploy to GKE

gcloud projects add-iam-policy-binding msds-432-g2-497302 \
  --member="serviceAccount:${SA}" \
  --role="roles/storage.admin"               # push to GCR

gcloud projects add-iam-policy-binding msds-432-g2-497302 \
  --member="serviceAccount:${SA}" \
  --role="roles/container.clusterViewer"     # get GKE credentials

# 3. Download the JSON key
gcloud iam service-accounts keys create sa-key.json \
  --iam-account=${SA}

# 4. Base64-encode it and copy to clipboard
base64 -w 0 sa-key.json | pbcopy    # macOS
base64 -w 0 sa-key.json | xclip     # Linux

# Paste the base64 string as the GCP_SA_KEY secret value
```

---

## Workflow Overview

```
Push to main
    │
    ├─► test job          (go vet + go test)
    │
    └─► build-push job    (matrix: ingestor | healthcheck | eda — all parallel)
             │   Builds with:
             │     docker build --build-arg SERVICE=<name>
             │       -t gcr.io/msds-432-g2-497302/resiliency-<name>:sha-<git-sha>
             │       -t gcr.io/msds-432-g2-497302/resiliency-<name>:latest
             │
             └─► deploy job
                   1. kubectl apply -f k8s/deployment.yaml
                   2. kubectl rollout status deployment/ingestor
                   3. kubectl rollout status deployment/healthcheck
```

**Pull Requests** only run the `pr-check` workflow (test + build, no push/deploy).

**Manual rollback** → Actions → Rollback → Run workflow → enter SHA.

---

## First-time GKE setup (run once before the first deploy)

```bash
# Authenticate locally
gcloud auth login
gcloud config set project msds-432-g2-497302

# Create GKE cluster (free tier: e2-micro nodes)
gcloud container clusters create resiliency-cluster \
  --zone us-east1-b \
  --num-nodes 2 \
  --machine-type e2-standard-2 \
  --enable-autoscaling --min-nodes 2 --max-nodes 6

# Get credentials
gcloud container clusters get-credentials resiliency-cluster --zone us-east1-b

# Create namespace (workflow does this too, but safe to run manually)
kubectl create namespace resiliency
```
