# Phase 3 Step 2 - Health Check + Region Health Service

This step adds the active-active health layer required by the Phase 2 design.

## New service

```text
cmd/healthcheck
internal/health
```

The service measures MongoDB connectivity and writes one record per monitored region to:

```text
resiliency_serving.region_health
```

Default monitored regions:

```text
us-east, us-west, europe, asia
```

## Local run order

```bash
go mod tidy
go build ./...

go run ./cmd/ingestor
go run ./cmd/transformer
go run ./cmd/aggregator
go run ./cmd/healthcheck
```

## Expected Atlas collections

```text
resiliency_raw.raw_github_events
resiliency_raw.raw_weather

resiliency_processed.github_events_flat
resiliency_processed.weather_flat

resiliency_serving.event_type_counts
resiliency_serving.regional_weather_agg
resiliency_serving.region_health
```

Expected `region_health` count:

```text
4 documents
```

## Important implementation note

During local development, only one MacBook is running the services. The healthcheck therefore uses the live MongoDB Atlas ping as the base latency and adds deterministic regional offsets to create stable records for the active-active dashboard. In a true GKE deployment, each region would run its own healthcheck pod with its own `REGION` value and write real measured latency for that region.

## Docker Compose

Step 2 enables four services:

```text
ingestor
transformer
aggregator
healthcheck
```

Run:

```bash
docker compose up --build
```

Stop:

```bash
docker compose down
```
