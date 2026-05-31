# Global Service Resiliency — Active-Active Continuity

> An end-to-end data engineering system that keeps operating when a primary data center
> fails — with no data loss and no user interruption.

**Course:** MSDS Data Engineering  
**University:** NorthWestern University, Masters In Data Science 
**Name** Grace Burns  
**Stack:** Go 1.22 · MongoDB Atlas · Google Kubernetes Engine · Streamlit · Prometheus · Grafana  
**Phases:** EDA → Detailed Design → Implementation (Docker Compose + GKE)

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Business Case](#2-business-case)
3. [Project Lifecycle — Phase Summary](#3-project-lifecycle--phase-summary)
4. [Phase 1 — Exploratory Data Analysis](#4-phase-1--exploratory-data-analysis)
5. [Phase 2 — System Design](#5-phase-2--system-design)
6. [Phase 3 — Implementation](#6-phase-3--implementation)
7. [System Architecture](#7-system-architecture)
8. [Data Pipeline](#8-data-pipeline)
9. [Repository Structure](#9-repository-structure)
10. [MongoDB Zone and Collection Reference](#10-mongodb-zone-and-collection-reference)
11. [Service Endpoints](#11-service-endpoints)
12. [Dashboard Reports](#12-dashboard-reports)
13. [Prerequisites](#13-prerequisites)
14. [Configuration](#14-configuration)
15. [Local Development — Docker Compose](#15-local-development--docker-compose)
16. [GKE Deployment](#16-gke-deployment)
17. [Validation Tests](#17-validation-tests)
18. [Known Limitations](#18-known-limitations)
19. [Implementation Status](#19-implementation-status)

---

## 1. Project Overview

This project implements a Go-based, multi-region active-active data platform
designed to answer a central question: **can we keep a production system running
when an entire regional data center fails?**

The system ingests live global event streams from two public APIs, stores data
across three MongoDB zones, serves pre-aggregated metrics through a REST API
gateway, and visualizes operational and business intelligence through Streamlit
and Grafana dashboards.

The implementation spans three phases: exploratory data analysis, detailed
architectural design, and full end-to-end implementation deployed on both local
Docker Compose and Google Kubernetes Engine.

---

## 2. Business Case

Global platforms in travel and financial technology sectors cannot afford
regional downtime. When a primary data center fails in a traditional
active-passive architecture, standby systems must be promoted — a process
that typically takes 45–60 seconds and results in measurable revenue loss,
customer attrition, and regulatory exposure.

This project implements the active-active alternative: all geographic regions
serve production traffic simultaneously. When one region fails, the remaining
healthy regions absorb its traffic without promotion delays. The system is
built on MongoDB's tunable consistency model, allowing explicit configuration
of the trade-off between regional read speed and global data freshness as
defined by the CAP Theorem.

**Five core engineering questions this project addresses:**

1. How quickly can the Go service detect a regional failure and reroute users?
2. How does Go driver configuration affect write latency when replicating across continents?
3. Which consistency level (LOCAL_QUORUM vs. EACH_QUORUM) balances speed and freshness?
4. How does the Go service handle network partitions to prevent data corruption?
5. How does multi-cluster Kubernetes deployment simplify active-active management?

**Five additional questions added during the design phase:**

6. What are the recovery-time (RTO) and recovery-point (RPO) objectives?
7. What does multi-region resilience cost at production scale?
8. How do we validate the active-active claim — not just diagram it?
9. How do we handle data quality issues from external APIs we do not control?
10. How does the system scale to 10x event volume without a redesign?

---

## 3. Project Lifecycle — Phase Summary

```
╔══════════════════════════════════════════════════════════════════════════════╗
║                    PROJECT PHASES — HIGH LEVEL FLOW                         ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  PHASE 1 ─────────────────────────────────────────────────────────────────  ║
║  Exploratory Data Analysis (EDA)                                             ║
║  │                                                                           ║
║  ├── Implemented in Go (Gota DataFrames + Gonum statistics)                 ║
║  ├── GitHub Events API: 300 events, 13 event types, 208 actors              ║
║  ├── OpenWeatherMap API: 20 cities across 4 regions                         ║
║  ├── Key findings: 27% PushEvent, ~17% bot share, 65% null org_login        ║
║  └── Output: Data gaps identified → shaped Phase 2 design decisions         ║
║                            │                                                 ║
║                            ▼                                                 ║
║  PHASE 2 ─────────────────────────────────────────────────────────────────  ║
║  Detailed System Design                                                      ║
║  │                                                                           ║
║  ├── Architecture: Data Lake + Data Ocean hybrid on GCP                     ║
║  ├── CAP positioning: CP system (Consistency over Availability)             ║
║  ├── 6 Go microservices designed and specified                              ║
║  ├── 3 MongoDB zones, 7 collections, 40+ STTM field mappings               ║
║  ├── 5 dashboard reports (RPT-01 through RPT-05) defined                   ║
║  └── Output: Architecture diagrams, STTM spreadsheet, design document       ║
║                            │                                                 ║
║                            ▼                                                 ║
║  PHASE 3 ─────────────────────────────────────────────────────────────────  ║
║  Implementation and Validation                                               ║
║  │                                                                           ║
║  ├── Steps 1–5:  Core Go services + Docker Compose local stack              ║
║  ├── Step 6:     Prometheus + Grafana observability layer                   ║
║  ├── Step 7:     Kubernetes manifests (Deployments, Services, ConfigMaps)   ║
║  ├── Step 8:     GKE deployment (API, dashboard, Prometheus, Grafana)       ║
║  ├── Step 9:     Controlled failover validation (application-level)         ║
║  ├── Step 10:    Fresh GKE redeploy, RTO 14.3s PASS, all 5 Grafana RPTs   ║
║  └── Step 11:    Cluster B (resiliency-gke-b) deployed in us-west1;         ║
║                  two-cluster active-active validated simultaneously         ║
║                                                                              ║
╚══════════════════════════════════════════════════════════════════════════════╝
```

---

## 4. Phase 1 — Exploratory Data Analysis

### 4.1 Approach

The EDA was implemented entirely in Go — a deliberate choice to ensure the
data structures, quality rules, and transformation logic from the analysis
phase carried directly into the production ingestion code. Python's Pandas
was replaced with the Gota DataFrame library; NumPy/SciPy was replaced with
the Gonum statistics package.

The EDA program executes three phases: concurrent data collection via
goroutines, DataFrame-based profiling using Gota, and statistical analysis
using Gonum.

### 4.2 GitHub Events API Findings

**Dataset:** 300 events, 13 event types, 208 unique actors, 240 unique repositories

| Event Type | Count | % Share |
|---|---|---|
| PushEvent | 81 | 27.0% |
| PullRequestEvent | 63 | 21.0% |
| IssuesEvent | 45 | 15.0% |
| IssueCommentEvent | 39 | 13.0% |
| WatchEvent | 17 | 5.7% |
| Other (8 types) | 55 | 18.3% |

**Key findings:**
- Code-related events account for **55.7%** of all activity
- Automated bots (github-actions, dependabot, Copilot, renovate) account for **~17%** of events
- **65.0%** of events have a null `org_login` (personal repository events)
- Activity is heavy-tailed: one repository contributed over 6% of the entire dataset

### 4.3 OpenWeatherMap API Findings

**Dataset:** 20 cities, 4 regions, 5 cities per region

| Region | Avg Temp (°C) | Avg Humidity | Avg Wind (m/s) |
|---|---|---|---|
| us-east | 26.25 | 45% | 6.43 |
| asia | 18.52 | 77% | 2.78 |
| us-west | 13.45 | 75% | 3.76 |
| europe | 13.42 | 80% | 3.60 |

**Key findings:**
- us-east is the warmest and driest (inland-southern city bias: Atlanta, Miami, Washington)
- europe and us-west are nearly identical despite very different climates
- asia shows the highest within-region variance (tropical Singapore vs. temperate Tokyo)

### 4.4 Data Gaps Identified and Resolved

| Gap | Resolution |
|---|---|
| 65% null `org_login` | Preserved in raw zone; flagged `MISSING_ORG` in processed zone |
| ~17% bot-driven events | `is_bot` field derived from login suffix during transform |
| Zero wind speed / visibility values | Flagged `ZERO_SENSOR_VALUE`; excluded from aggregations |
| Visibility API capping at 10,000m | Flagged `CAPPED_VISIBILITY`; preserved in raw zone |
| URL encoding failure for multi-word city names | Fixed with `url.QueryEscape()` in Go collector |

---

## 5. Phase 2 — System Design

### 5.1 Architecture Decision

A hybrid architecture combining a Data Lake (for raw storage of variable-schema
JSON payloads) with Data Ocean governance principles (regional autonomy in data
processing) was selected over a Data Warehouse (too rigid for semi-structured
event streams). GCP was chosen over local or hybrid deployment because
simulating CAP Theorem partition scenarios requires real geographic network
infrastructure.

### 5.2 CAP Theorem Position

This system is a **CP system** — it favors Consistency over Availability during
a network partition. Regions that lose quorum stop accepting writes rather than
risk split-brain data conflicts.

| Write Path | Write Concern | Rationale |
|---|---|---|
| Raw ingestion | `w:majority` | Source-of-truth data; cannot be lost |
| Processed zone | `w:majority` | Analyst-ready data must be consistent |
| Health heartbeats | `w:1` | High-frequency; losing one 30s heartbeat is acceptable |
| Serving aggregations | `w:1` | Reproducible from source; speed prioritized |

| Read Path | Read Preference | Rationale |
|---|---|---|
| Dashboard reads | `nearest` | Lowest latency for end users |
| Analytics | `secondaryPreferred` | Offloads primary node |
| Health checks | `primary` | Must reflect current cluster state |

### 5.3 Six Services Designed

| Service | Responsibility |
|---|---|
| Ingestor | Concurrent API polling → raw zone |
| Transformer | Flatten, enrich, quality flags → processed zone |
| Aggregator | Pre-compute aggregations → serving zone |
| Health-Check | Ping MongoDB Atlas → `region_health` every 30s |
| Traffic Router | Read `region_health` → select preferred + fallback → `routing_decisions` |
| API Gateway | chi/mux REST gateway → 8 endpoints → Streamlit + Grafana |

### 5.4 Five Reports Defined

| Report | Source Collection |
|---|---|
| RPT-01: GitHub Event Type Distribution | `event_type_counts` |
| RPT-02: Developer Activity Composition | `event_type_counts` (category-grouped) |
| RPT-03: Regional Weather Dashboard | `regional_weather_agg` |
| RPT-04: Active-Active Region Health Monitor | `region_health` |
| RPT-05: Routing Decision and Continuity Signal | `routing_decisions` |

---

## 6. Phase 3 — Implementation

### 6.1 Steps

| Step | Description |
|---|---|
| Steps 1–5 | All 6 Go services + full Docker Compose local stack |
| Step 6 | Prometheus `/metrics` endpoints + Grafana observability dashboard |
| Step 7 | Kubernetes manifests (Namespace, ConfigMap, Secret template, Deployments, Services) |
| Step 8 | GKE cluster creation, Artifact Registry image push, full deployment validation |
| Step 9 | Controlled failover: preferred region marked down, router avoided failure — PASS |
| Step 10 | Fresh GKE redeploy with step10 images, RTO 14.3s PASS, all 5 Grafana dashboards, external LoadBalancer |
| Step 11 | Cluster B (`resiliency-gke-b`) deployed in us-west1-a using same manifests; REGION ConfigMap patched to us-west1; both clusters Running 9/9 pods simultaneously — two-cluster active-active validated |

### 6.2 Final Validation Results

| Validation | Result |
|---|---|
| All 9 GKE pods Running (READY 1/1, RESTARTS 0) | ✅ PASS |
| Prometheus — all 6 application targets UP | ✅ PASS |
| Grafana — all 5 RPT dashboards rendered externally | ✅ PASS |
| Streamlit — all 5 reports rendered via LoadBalancer | ✅ PASS |
| API pod recovery (RTO) | ✅ **PASS — 14.3 seconds** (target ≤ 60s) |
| Controlled region failover | ✅ **PASS** — router selected healthy fallback |
| Cluster B deployed (us-west1) | ✅ **PASS** — 9/9 pods Running, RESTARTS 0 |
| Two clusters running simultaneously | ✅ **PASS** — `resiliency-gke` (us-central1) + `resiliency-gke-b` (us-west1) |
| Independent regional identity | ✅ **PASS** — Cluster A `REGION=us-central1`, Cluster B `REGION=us-west1` |
| Application-layer routing on both clusters | ✅ **PASS** — each cluster independently queries region_health and returns routing decisions |

---

## 7. System Architecture

```
  Data Sources
  ┌─────────────────────────┐     ┌──────────────────────────────┐
  │  GitHub Events API      │     │  OpenWeatherMap API           │
  │  Public event stream    │     │  20 cities · 4 regions       │
  └────────────┬────────────┘     └───────────────┬──────────────┘
               │                                  │
               └──────────────────┬───────────────┘
                                  │  concurrent goroutines
                                  ▼
  ┌─────────────────────────────────────────────────────────────────────────────┐
  │                        TWO-CLUSTER ACTIVE-ACTIVE DEPLOYMENT                 │
  ├──────────────────────────────────────┬──────────────────────────────────────┤
  │  CLUSTER A: resiliency-gke           │  CLUSTER B: resiliency-gke-b         │
  │  Location : us-central1-a  ← ACTIVE │  Location : us-west1-a     ← ACTIVE  │
  │  REGION   : us-central1              │  REGION   : us-west1                 │
  │                                      │                                      │
  │  ┌──────────┐ ┌───────────┐          │  ┌──────────┐ ┌───────────┐         │
  │  │ ingestor │ │transformer│          │  │ ingestor │ │transformer│         │
  │  │ :8081    │ │ :8082     │          │  │ :8081    │ │ :8082     │         │
  │  └────┬─────┘ └─────┬─────┘          │  └────┬─────┘ └─────┬─────┘         │
  │  ┌──────────┐ ┌───────────┐          │  ┌──────────┐ ┌───────────┐         │
  │  │aggregator│ │healthcheck│          │  │aggregator│ │healthcheck│         │
  │  │ :8085    │ │ :8083     │          │  │ :8085    │ │ :8083     │         │
  │  └──────────┘ └─────┬─────┘          │  └──────────┘ └─────┬─────┘         │
  │  ┌──────────┐ ┌───────────┐          │  ┌──────────┐ ┌───────────┐         │
  │  │  router  │ │    api    │          │  │  router  │ │    api    │         │
  │  │ :8084    │ │ :8080     │          │  │ :8084    │ │ :8080     │         │
  │  └──────────┘ └───────────┘          │  └──────────┘ └───────────┘         │
  │  ┌──────────┐ ┌───────────┐          │  ┌──────────┐ ┌───────────┐         │
  │  │prometheus│ │ dashboard │          │  │prometheus│ │ dashboard │         │
  │  │ :9090    │ │ :8501     │          │  │ :9090    │ │ :8501     │         │
  │  └──────────┘ │  grafana  │          │  └──────────┘ │  grafana  │         │
  │               │ :3000 LB  │          │               │ :3000 LB  │         │
  │               └───────────┘          │               └───────────┘         │
  │                                      │                                      │
  │  Each cluster independently:         │  Each cluster independently:         │
  │  · measures Atlas latency            │  · measures Atlas latency            │
  │  · writes to region_health           │  · writes to region_health           │
  │  · selects routing preference        │  · selects routing preference        │
  └──────────────────────────────────────┴──────────────────────────────────────┘
                                  │
                                  ▼
  ┌─────────────────────────────────────────────────────────────────────────────┐
  │                  MONGODB ATLAS (M0 — shared by both clusters)               │
  │                                                                             │
  │  resiliency_raw          resiliency_processed      resiliency_serving       │
  │  ├─ raw_github_events    ├─ github_events_flat     ├─ event_type_counts     │
  │  └─ raw_weather          └─ weather_flat           ├─ regional_weather_agg  │
  │                                                    ├─ region_health         │
  │                                                    └─ routing_decisions     │
  │                                                                             │
  │  Note: M0 is single-region. Multi-region Atlas replica set (M10+)           │
  │  is the production-grade path for true cross-continental replication.        │
  └─────────────────────────────────────────────────────────────────────────────┘
```

---

## 8. Data Pipeline

```
  STAGE 1: EXTRACT
  ├── GitHub Events API → Go goroutines (sync.WaitGroup)
  │   ├── 10 pages × 100 events · authenticated (5,000 req/hr)
  │   └── HTTP validation + exponential backoff on errors
  └── OpenWeatherMap API → Go goroutines (concurrent per city)
      ├── 20 cities across 4 regions
      └── url.QueryEscape() fixes multi-word city names (Phase 1 bug fix)
              │
              ▼
  STAGE 2: RAW LOAD  (resiliency_raw)
  ├── write concern: w:majority
  ├── idempotent upserts — no duplicates across runs
  ├── full lineage: ingested_at, batch_id, region
  └── immutable — never modified after write
              │
              ▼
  STAGE 3: TRANSFORM
  ├── GitHub: flatten actor.login, extract hour/day_of_week,
  │           detect bots, flag MISSING_ORG, type-cast all fields
  └── Weather: Kelvin → Celsius, flag ZERO_SENSOR_VALUE,
               flag CAPPED_VISIBILITY, assign region tag
              │
              ▼
  STAGE 4: PROCESSED LOAD  (resiliency_processed)
  ├── write concern: w:majority
  ├── indexes: type+created_at, region+observed_at, quality_flag
  └── idempotent upserts keyed on event_id / city_name+observed_at
              │
              ▼
  STAGE 5: AGGREGATE → SERVE  (resiliency_serving)
  ├── event_type_counts:     GROUP BY type → COUNT(*), PERCENTAGE
  ├── regional_weather_agg:  GROUP BY region → AVG(temp, humidity, wind)
  ├── region_health:         Atlas ping latency per region — every 30s
  └── routing_decisions:     preferred + fallback selection — on each refresh
              │
              ▼
  STAGE 6: SERVE AND VISUALIZE
  ├── REST API Gateway (chi/mux) → Streamlit (RPT-01 to RPT-05)
  ├── REST API Gateway (chi/mux) → Grafana  (RPT-01 to RPT-05)
  └── Prometheus → scrapes /metrics from all 6 Go services every 15s
```

---

## 9. Repository Structure

```
resiliency/
│
├── cmd/                              Service entry points
│   ├── ingestor/main.go
│   ├── transformer/main.go
│   ├── aggregator/main.go
│   ├── healthcheck/main.go
│   ├── router/main.go
│   └── api/main.go
│
├── internal/                         Shared packages
│   ├── config/config.go              Typed config from environment variables
│   ├── db/client.go                  MongoDB client, collection helpers, write concerns
│   ├── models/raw.go                 RawGitHubEvent, RawWeather
│   ├── models/processed.go           GitHubEventFlat, WeatherFlat, RegionHealth, etc.
│   ├── github/collector.go           Concurrent GitHub Events API ingestion
│   ├── weather/collector.go          Concurrent OpenWeatherMap ingestion
│   ├── transform/transformer.go      Raw-to-processed transformation logic
│   ├── aggregator/aggregator.go      MongoDB aggregation pipelines
│   ├── health/checker.go             Atlas ping + latency measurement
│   └── router/router.go              Routing decision logic
│
├── deployments/
│   ├── docker/                       Dockerfiles (one per service) + prometheus.yml
│   ├── grafana/
│   │   ├── dashboards/               Dashboard JSON files (rpt01 through rpt05)
│   │   └── provisioning/             Grafana datasource and dashboard provider YAML
│   ├── k8s/                          Kubernetes manifests (applied in numeric order)
│   │   ├── 00-namespace.yaml
│   │   ├── 01-secrets.yaml.template  <- never committed with real values
│   │   ├── 02-configmap.yaml
│   │   ├── 03-prometheus-config.yaml
│   │   ├── 04-ingestor.yaml through 12-grafana.yaml
│   │   └── 13 through 15-grafana-dashboards-json.yaml
│   └── gke/gke.env                   GKE cluster configuration
│
├── scripts/
│   ├── step10_build.sh                  Build + push :step10 images to Artifact Registry
│   ├── step10_deploy.sh                 Create Cluster A (us-central1) + apply all manifests
│   ├── step10_deploy_cluster_b.sh       Create Cluster B (us-west1) + patch REGION=us-west1
│   ├── step10_evidence_both_clusters.sh Capture simultaneous pod status + routing from both clusters
│   ├── step10_rto_test.sh               RTO pod recovery + controlled failover tests
│   └── setup_git.sh                     One-time Git repository initialization
│
├── docker-compose.yml                Full local stack — all 9 services active
├── go.mod                            Module: github.com/resiliency/global
├── .env.example                      Environment variable template
├── .gitignore                        Excludes .env and generated secrets
└── README.md                         This file
```

---

## 10. MongoDB Zone and Collection Reference

```
  resiliency_raw           resiliency_processed       resiliency_serving
  (immutable)              (analyst-ready)             (dashboard-optimized)
  ├─ raw_github_events     ├─ github_events_flat       ├─ event_type_counts
  └─ raw_weather           └─ weather_flat             ├─ regional_weather_agg
                                                       ├─ region_health
                                                       └─ routing_decisions
  w:majority writes        w:majority writes           w:1 writes
  primary reads            nearest reads               nearest reads
```

### Key Fields by Collection

**`raw_github_events`** — verbatim API payload, upsert key: `event_id`

| Field | Type | Notes |
|---|---|---|
| event_id | String | GitHub unique event ID |
| type | String | PushEvent, PullRequestEvent, IssuesEvent, etc. |
| actor | Document | login, id, avatar_url |
| repo | Document | name, id, url |
| org | Document | Nullable for personal repository events |
| payload | Document | Variable schema stored as BSON document |
| ingested_at | ISODate | Pipeline lineage timestamp |
| batch_id | String | Collection run identifier |

**`github_events_flat`** — enriched, upsert key: `event_id`

| Field | Type | Notes |
|---|---|---|
| actor_login | String | Flattened from actor.login |
| is_bot | Boolean | Derived: true if login ends in [bot] or -bot |
| org_login | String | Nullable — flagged MISSING_ORG when absent |
| hour | Int | 0–23 UTC, extracted from created_at |
| quality_flag | String | OK or MISSING_ORG |

**`region_health`** — heartbeat, upsert key: `region`

| Field | Type | Notes |
|---|---|---|
| region | String | asia, europe, us-east, us-west |
| status | String | healthy, degraded, or down |
| latency_ms | Int | Measured Atlas ping round-trip |
| replication_lag_ms | Int | Estimated (2 × latency) |
| write_concern | String | Configured write concern label |
| checked_at | ISODate | Timestamp of last health check |

**`routing_decisions`** — current routing state, upsert key: `decision_id = "current"`

| Field | Type | Notes |
|---|---|---|
| preferred_region | String | Lowest-latency healthy region |
| preferred_latency_ms | Int | Ping latency of preferred region |
| fallback_region | String | Second-lowest-latency healthy region |
| healthy_count | Int | Healthy regions at decision time |
| down_count | Int | Down regions at decision time |
| reason | String | Human-readable decision rationale |

---

## 11. Service Endpoints

| Service | Port | Key Endpoints |
|---|---|---|
| **api** | 8080 | `GET /health` · `/api/summary` · `/api/events/types` · `/api/events/activity-categories` · `/api/weather/regions` · `/api/regions/health` · `/api/routing/current` · `POST /api/router/refresh` · `GET /metrics` |
| **router** | 8084 | `GET /health` · `/route` · `POST /refresh` · `/metrics` |
| **healthcheck** | 8083 | `GET /health` · `/metrics` |
| **ingestor** | 8081 | `GET /health` · `/metrics` |
| **transformer** | 8082 | `GET /health` · `/metrics` |
| **aggregator** | 8085 | `GET /health` · `/metrics` |
| **dashboard** | 8501 | Streamlit web UI |
| **prometheus** | 9090 | Prometheus UI + `/targets` |
| **grafana** | 3000 | Dashboard UI — admin / resiliency |

---

## 12. Dashboard Reports

All five reports are available in both Streamlit and Grafana, reading from the
same REST API endpoints backed by the MongoDB serving zone.

| Report | Description | API Endpoint |
|---|---|---|
| **RPT-01** GitHub Event Type Distribution | All event types by count and percentage. PushEvent leads at ~30%. | `/api/events/types` |
| **RPT-02** Developer Activity Composition | Event types grouped into 5 categories: Code Change, Issue Collaboration, Code Review, Repository Activity, Community/Watch. | `/api/events/activity-categories` |
| **RPT-03** Regional Weather Dashboard | Average temperature, humidity, and wind speed for all 4 regions. | `/api/weather/regions` |
| **RPT-04** Active-Active Region Health Monitor | Region status (healthy/degraded/down), ping latency, replication lag, write concern. | `/api/regions/health` |
| **RPT-05** Routing Decision and Continuity Signal | Preferred and fallback regions, their latencies, and routing decision metadata. | `/api/routing/current` |

---

## 13. Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.22+ | Build services locally |
| Docker Desktop | Latest | Local container stack |
| kubectl | Latest | GKE cluster management |
| gcloud CLI | Latest | GCP authentication and cluster control |
| gke-gcloud-auth-plugin | Latest | Required for kubectl → GKE authentication |
| mongosh | Latest | MongoDB shell for failover test scripts |

---

## 14. Configuration

```bash
cp .env.example .env
```

```bash
# Required
MONGODB_URI=mongodb+srv://<user>:<password>@cluster0.d3whiqa.mongodb.net/
GITHUB_TOKEN=<your_github_personal_access_token>
OWM_API_KEY=<your_openweathermap_api_key>

# Defaults
REGION=us-east1
INGESTOR_INTERVAL_SECONDS=300
TRANSFORMER_INTERVAL_SECONDS=600
AGGREGATOR_INTERVAL_SECONDS=900
HEALTHCHECK_INTERVAL_SECONDS=30
```

> `.env` is in `.gitignore` and must never be committed. The Kubernetes Secret
> is generated at deploy time by `step10_deploy.sh` from your local `.env`.

---

## 15. Local Development — Docker Compose

```bash
# First-time setup
go mod tidy
go build ./...                    # verify all services compile

# Start full stack
docker compose up --build

# Verify health (in a second terminal)
docker compose ps
curl http://localhost:8081/health  # ingestor
curl http://localhost:8082/health  # transformer
curl http://localhost:8083/health  # healthcheck
curl http://localhost:8084/health  # router
curl http://localhost:8085/health  # aggregator
curl http://localhost:8080/health  # api
curl http://localhost:8084/route   # routing decision

# Dashboards
open http://localhost:8501         # Streamlit
open http://localhost:9090/targets # Prometheus (all 6 targets UP)
open http://localhost:3000         # Grafana (admin / resiliency)

# Stop
docker compose down
```

### External demo (local, temporary)

```bash
docker compose up -d healthcheck router api dashboard
ngrok http 8501
```

---

## 16. GKE Deployment

### Cluster A — Primary active region (us-central1)

```bash
# Authenticate
gcloud auth login
gcloud config set project dataengineering-496300
gcloud auth configure-docker us-central1-docker.pkg.dev

# Build and push step10 images (only needed once — both clusters use the same images)
./scripts/step10_build.sh

# Deploy Cluster A: resiliency-gke in us-central1-a
# Creates cluster, generates Secret from .env, applies all 15 manifests, waits for pods
./scripts/step10_deploy.sh
# Script pauses — update MongoDB Atlas Network Access with Cluster A node IPs before continuing

# Verify Cluster A
kubectl get pods -n resiliency -o wide          # all 9 pods Running, RESTARTS 0
kubectl get svc grafana dashboard -n resiliency  # get external IPs
```

### Cluster B — Second active region (us-west1)

```bash
# Deploy Cluster B: resiliency-gke-b in us-west1-a
# Applies same manifests, patches ConfigMap so REGION=us-west1
./scripts/step10_deploy_cluster_b.sh
# Script pauses — add Cluster B node IPs to Atlas Network Access as well

# Verify Cluster B
kubectl config use-context gke_dataengineering-496300_us-west1-a_resiliency-gke-b
kubectl get pods -n resiliency -o wide          # all 9 pods Running, RESTARTS 0
```

### Confirm both clusters running simultaneously

```bash
# Both clusters appear in one command
gcloud container clusters list --project dataengineering-496300

# Capture full two-cluster evidence (routing decisions from each cluster)
./scripts/step10_evidence_both_clusters.sh
# Evidence saved to: deployments/chaos/evidence/step11/

# Switch between clusters
kubectl config use-context gke_dataengineering-496300_us-central1-a_resiliency-gke
kubectl config use-context gke_dataengineering-496300_us-west1-a_resiliency-gke-b
```

### Teardown both clusters (avoid charges when not in use)

```bash
gcloud container clusters delete resiliency-gke \
  --zone us-central1-a --project dataengineering-496300 --quiet

gcloud container clusters delete resiliency-gke-b \
  --zone us-west1-a --project dataengineering-496300 --quiet
```

---

## 17. Validation Tests

```bash
# Start port-forwards
kubectl port-forward -n resiliency svc/api 8080:8080 &
kubectl port-forward -n resiliency svc/router 8084:8084 &

# Run RTO + failover tests
./scripts/step10_rto_test.sh
```

**Test A — API pod recovery (RTO ≤ 60s):**

| Step | Measured | Status |
|---|---|---|
| Step 10 | 14.3 seconds | **PASS** |

**Test B — Controlled region failover:**

| Before | After | Status |
|---|---|---|
| Preferred: europe | Preferred: us-east (asia as fallback) | **PASS** |

Evidence files are written to `deployments/chaos/evidence/step10/`.

---

## 18. Known Limitations

| Limitation | Current State | Phase 4 Path |
|---|---|---|
| **GCP Global Load Balancer** | Both clusters deployed; Anycast DNS routing across them is not yet configured. External access is via individual LoadBalancer IPs per cluster. | Configure GCP GCLB with backend services pointing to both clusters; enable Anycast geo-routing. |
| **Estimated replication lag** | `replication_lag_ms` = 2 × ping latency. True measurement requires Atlas M10+ and optime comparison or the Atlas Metrics API. | Upgrade to Atlas M10+; integrate Atlas Monitoring API. |
| **Application-level failover** | Failover test patches `region_health` directly. No Chaos Mesh infrastructure-level experiments performed. | Deploy Chaos Mesh on GKE; run network partition and node failure experiments. |
| **Single-region Atlas M0** | Free tier; single-region only. Both clusters connect to the same Atlas instance. Design specifies 3-node multi-region replica set. | Upgrade to Atlas M10+; configure Primary (us-east) + Secondary (us-west) + Tiebreaker (us-central1). |
| **RPT-02 activity composition** | Implemented as category grouping, not hour-by-day heatmap. | Add `/api/events/hourly` endpoint aggregating by hour over a rolling window. |
| **No CI/CD pipeline** | Manual deployment via shell scripts. | GitHub Actions: build → push to Artifact Registry → `kubectl rollout restart`. |

---

## 19. Implementation Status

| Component | Status |
|---|---|
| All 6 Go services | ✅ Complete |
| MongoDB 3 zones / 7 collections | ✅ Complete |
| Docker Compose local stack | ✅ Complete |
| Prometheus — all 6 targets UP | ✅ Complete |
| Grafana — all 5 RPT dashboards | ✅ Complete |
| Streamlit — all 5 reports | ✅ Complete |
| Kubernetes manifests | ✅ Complete |
| GKE Cluster A deployment (us-central1) | ✅ Complete — 9 pods Running, 0 restarts |
| GKE Cluster B deployment (us-west1) | ✅ Complete — 9 pods Running, 0 restarts |
| Two-cluster active-active | ✅ Complete — both clusters Running simultaneously, independent REGION identity |
| External LoadBalancer access | ✅ Complete — Grafana + Streamlit on both clusters |
| RTO validation | ✅ **PASS — 14.3 seconds** (target ≤ 60s) |
| Controlled failover | ✅ **PASS** |
| Source control | ✅ Complete — committed to GitHub |
| GCP Global Load Balancer (Anycast) | 🔵 Future — GCLB config across both clusters |
| Real Atlas replication lag | 🔵 Future — requires M10+ Atlas cluster |
| Chaos Mesh experiments | 🔵 Future — infrastructure-level chaos testing |

---

*Global Service Resiliency — MSDS Data Engineering  Project · May 2026*  
*Grace Burns*
