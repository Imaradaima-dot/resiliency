# Phase 3 Step 4 - REST API Gateway

This step adds a read-only API Gateway over the serving-zone collections. It keeps dashboard clients from connecting directly to MongoDB and provides one stable backend contract for Streamlit, Grafana, or future UI components.

## New service

```text
cmd/api
internal/api
```

The service listens on `API_PORT`, default `8080`.

## Source collections

The API Gateway reads from:

```text
resiliency_serving.event_type_counts
resiliency_serving.regional_weather_agg
resiliency_serving.region_health
resiliency_serving.routing_decisions
```

## Endpoints

```text
GET /health
GET /api/events/types
GET /api/weather/regions
GET /api/regions/health
GET /api/routing/current
GET /api/summary
```

## Run order for local testing

Run the upstream services first so the serving collections exist and contain fresh data:

```bash
go run ./cmd/ingestor
go run ./cmd/transformer
go run ./cmd/aggregator
go run ./cmd/healthcheck
go run ./cmd/router
```

Then start the API Gateway:

```bash
go run ./cmd/api
```

## Curl validation

```bash
curl http://localhost:8080/health
curl http://localhost:8080/api/events/types
curl http://localhost:8080/api/weather/regions
curl http://localhost:8080/api/regions/health
curl http://localhost:8080/api/routing/current
curl http://localhost:8080/api/summary
```

Expected result: each endpoint returns JSON. `/api/summary` should show total event count, event type count, weather region count, health region count, and the current preferred/fallback routing decision.

## Docker Compose

After `go mod tidy` creates `go.sum`, run:

```bash
docker compose up --build
```

Active services now include:

```text
ingestor
transformer
aggregator
healthcheck
router
api
```

Prometheus and Grafana remain commented out until `/metrics` endpoints and dashboard provisioning are added in a later step.
