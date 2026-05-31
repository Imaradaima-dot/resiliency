# Step 3 - Traffic Routing Service

This step adds the active-active routing layer for the Global Service Resiliency project.

## What was added

- `cmd/router/main.go` - router service entry point
- `internal/routing/router.go` - routing decision logic
- `resiliency_serving.routing_decisions` - new serving-zone collection
- `/health` endpoint on port `8084`
- `/route` endpoint to view the current routing decision
- `/route/refresh` endpoint to force a new decision cycle

## What the router does

1. Reads current region health rows from `resiliency_serving.region_health`.
2. Filters out stale health rows older than `ROUTING_MAX_AGE_SECONDS`.
3. Selects the lowest-latency `healthy` region as preferred.
4. Selects the next best healthy or degraded region as fallback.
5. Writes the current decision to `resiliency_serving.routing_decisions`.
6. Exposes the decision through `GET /route`.

## Required collections before running

Make sure Step 2 has already populated:

```text
resiliency_serving.region_health
```

Expected count:

```text
4 documents
```

## Environment variables

Add these to `.env` if missing:

```bash
ROUTER_PORT=8084
ROUTER_INTERVAL_SECONDS=30
ROUTING_MAX_AGE_SECONDS=120
MONITORED_REGIONS=us-east,us-west,europe,asia
```

## Run locally

```bash
go mod tidy
go build ./...
go run ./cmd/router
```

Expected logs:

```text
router starting
router endpoint listening addr=:8084
routing cycle complete preferred_region=<region> fallback_region=<region>
routing done preferred_region=<region>
```

## Validate in Atlas

Check:

```text
resiliency_serving.routing_decisions
```

Expected count:

```text
1 document
```

The document should contain:

- `decision_id: current`
- `preferred_region`
- `preferred_status`
- `preferred_latency_ms`
- `fallback_region`
- `reason`
- `decided_at`

## Validate with curl

In a second terminal:

```bash
curl http://localhost:8084/health
curl http://localhost:8084/route
curl -X POST http://localhost:8084/route/refresh
```

## Docker Compose

After `go mod tidy` creates `go.sum`:

```bash
docker compose up --build
```

Active services in Step 3:

- ingestor
- transformer
- aggregator
- healthcheck
- router

## Why this matters

This service connects the data engineering pipeline to the active-active continuity requirement. The system now has a machine-readable routing decision based on health status, latency, and freshness of regional health checks.
