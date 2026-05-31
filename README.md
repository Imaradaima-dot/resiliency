cat > README.md <<'EOF'
# Global Service Resiliency - Phase 3 Implementation

This repository contains the Phase 3 implementation of the Global Service Resiliency project. The system demonstrates a containerized active-active resiliency architecture using Go microservices, MongoDB Atlas, Docker Compose, Google Kubernetes Engine (GKE), Prometheus, Grafana, and a Streamlit reporting dashboard.

The final Step 10 implementation validates both local execution and cloud-hosted execution on GKE. The GKE version exposes Grafana and Streamlit through LoadBalancer services so the dashboards can be accessed by external IP address.

---

## 1. Architecture Overview

The implementation uses a raw, processed, and serving-zone pattern backed by MongoDB Atlas.

| Layer | Purpose | Collections |
|---|---|---|
| Raw Zone | Stores raw GitHub Events and OpenWeatherMap data | `raw_github_events`, `raw_weather` |
| Processed Zone | Stores flattened and enriched records | `github_events_flat`, `weather_flat` |
| Serving Zone | Stores dashboard-ready outputs | `event_type_counts`, `regional_weather_agg`, `region_health`, `routing_decisions` |

The application is composed of six Go services plus dashboards and observability services.

| Service | Port | Responsibility |
|---|---:|---|
| `ingestor` | 8081 | Collects GitHub Events and weather data |
| `transformer` | 8082 | Flattens and enriches raw records |
| `aggregator` | 8085 | Creates dashboard-ready aggregates |
| `healthcheck` | 8083 | Writes active-active region health and latency signals |
| `router` | 8084 | Selects preferred and fallback regions |
| `api` | 8080 | Exposes REST endpoints for Streamlit and Grafana |
| `dashboard` | 8501 | Streamlit reporting dashboard |
| `prometheus` | 9090 | Scrapes service metrics |
| `grafana` | 3000 | Renders operational and report dashboards |

---

## 2. Final Validation Status

The final Step 10 implementation validates:

- Local Docker Compose runtime
- MongoDB Atlas raw, processed, and serving-zone persistence
- REST API Gateway endpoints
- Streamlit dashboard reports RPT-01 through RPT-05
- Prometheus scraping of all six Go services
- Grafana report dashboards RPT-01 through RPT-05
- GKE deployment using Kubernetes Deployments, Services, ConfigMaps, and Secrets
- External dashboard access through GCP LoadBalancer services
- API pod recovery against a 60-second RTO target
- Controlled failover where the router avoids a failed preferred region

Final RTO validation result:

```text
Recovery time: 14.3 seconds
SLA target: <= 60 seconds
Result: PASS
