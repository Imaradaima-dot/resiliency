# Global Service Resiliency — Go Implementation

Active-active multi-region data engineering system built on **Go + MongoDB**.  
Ingests from the GitHub Events API and OpenWeatherMap API, stores data across  
three MongoDB zones (Raw → Processed → Serving), and exposes HTTP health probes.

---

## Project Structure

```
resiliency/
├── cmd/
│   ├── ingestor/      # ETL pipeline (Extract → Load → Transform → Aggregate)
│   ├── healthcheck/   # Liveness/readiness probes + background MongoDB pinger
│   └── eda/           # Exploratory Data Analysis report + CSV export
├── internal/
│   ├── config/        # Environment variable loader
│   ├── db/            # MongoDB client, collection accessors, index management
│   ├── ingestion/     # GitHub Events collector, OpenWeatherMap collector
│   ├── transform/     # Flatten, enrich, quality-flag, aggregate
│   ├── health/        # Health checker (HTTP + background goroutine)
│   └── models/        # Typed structs for all 7 collections (3 zones)
├── k8s/               # Kubernetes manifests (GKE)
├── scripts/           # MongoDB init script
├── Dockerfile         # Multi-stage build (ARG SERVICE=ingestor|healthcheck|eda)
├── docker-compose.yml # Local dev stack with MongoDB replica set
└── .env.example       # Configuration reference
```

---

## Quick Start (Docker Compose — no API keys needed)

```bash
# 1. Clone and enter the project
git clone <your-repo>
cd resiliency

# 2. Copy and configure environment variables
cp .env.example .env
# Edit .env and add GITHUB_TOKEN and OWM_API_KEY (optional for offline mode)

# 3. Start MongoDB + services
docker-compose up --build

# 4. Run EDA report (separate terminal)
docker-compose --profile tools run --rm eda
```

Services started:
- **MongoDB** on `localhost:27017` (single-node replica set)
- **Ingestor** runs every 5 minutes (configurable via `INGEST_INTERVAL`)
- **Health-check** on `http://localhost:8080`

---

## Running Locally (without Docker)

Prerequisites: Go 1.22+, MongoDB 7.0 running locally

```bash
# Install dependencies
go mod download

# Copy env file and source it
cp .env.example .env
source .env
export MONGO_URI="replace_with_mongodb_connection_string"

# Run the ingestor (one-shot cycle)
go run ./cmd/ingestor

# Run the health-check service
go run ./cmd/healthcheck

# Run the EDA report
go run ./cmd/eda
```

---

## Health Check Endpoints

| Endpoint  | Purpose                         | K8s Probe   |
|-----------|---------------------------------|-------------|
| `GET /livez`  | Process alive?              | Liveness    |
| `GET /readyz` | MongoDB reachable?          | Readiness   |
| `GET /status` | Region health detail (JSON) | Dashboard   |

```bash
curl http://localhost:8080/livez
curl http://localhost:8080/readyz
curl http://localhost:8080/status
```

---

## MongoDB Zone Architecture

| Zone              | Database               | Collections                        |
|-------------------|------------------------|------------------------------------|
| Raw (immutable)   | `resiliency_raw`       | `raw_github_events`, `raw_weather` |
| Processed         | `resiliency_processed` | `github_events_flat`, `weather_flat` |
| Serving           | `resiliency_serving`   | `event_type_counts`, `regional_weather_agg`, `region_health` |

---

## CAP Theorem Configuration

Tune the consistency/availability trade-off via environment variables:

```bash
# Strongest consistency (cross-region durability, higher write latency)
MONGO_WRITE_CONCERN=majority
MONGO_READ_PREF=nearest

# Fastest writes (local durability only — use for experimentation)
MONGO_WRITE_CONCERN=1
MONGO_READ_PREF=primaryPreferred
```

---

## GKE Deployment

```bash
# Build and push images
docker build --build-arg SERVICE=ingestor -t gcr.io/YOUR_PROJECT/resiliency-ingestor .
docker build --build-arg SERVICE=healthcheck -t gcr.io/YOUR_PROJECT/resiliency-healthcheck .
docker build --build-arg SERVICE=eda -t gcr.io/YOUR_PROJECT/resiliency-eda .
docker push gcr.io/YOUR_PROJECT/resiliency-ingestor
docker push gcr.io/YOUR_PROJECT/resiliency-healthcheck
docker push gcr.io/YOUR_PROJECT/resiliency-eda

# Create secrets
kubectl create secret generic resiliency-secrets \
  --from-literal=MONGO_URI="replace_with_mongodb_connection_string" \
  --from-literal=GITHUB_TOKEN="replace_with_github_token" \
  --from-literal=OWM_API_KEY="replace_with_openweathermap_api_key" \
  -n resiliency

# Deploy
kubectl apply -f k8s/deployment.yaml
```

---

## Environment Variables Reference

| Variable            | Default         | Description                                      |
|---------------------|-----------------|--------------------------------------------------|
| `MONGO_URI`         | `localhost:27017` | MongoDB connection string                      |
| `MONGO_DATABASE`    | `resiliency`    | Base database name (zones are suffixed)          |
| `MONGO_WRITE_CONCERN` | `majority`    | `majority` / `1` / `2`                           |
| `MONGO_READ_PREF`   | `nearest`       | `nearest` / `primary` / `secondaryPreferred`     |
| `GITHUB_TOKEN`      | *(empty)*       | GitHub PAT (5000 req/hr vs 60 unauthenticated)   |
| `GITHUB_PAGES`      | `3`             | Pages per ingest cycle (max 10)                  |
| `OWM_API_KEY`       | *(required)*    | OpenWeatherMap API key                           |
| `INGEST_INTERVAL`   | `5m`            | Ingestor cycle interval                          |
| `HEALTH_INTERVAL`   | `30s`           | Health-check ping interval                       |
| `HEALTH_PORT`       | `8080`          | HTTP port for health probes                      |
| `OFFLINE_MODE`      | `false`         | Load from local JSON instead of live APIs        |
| `RAW_DATA_DIR`      | `./data/raw`    | Directory for raw JSON snapshots                 |
