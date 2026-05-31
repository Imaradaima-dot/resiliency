# Step 6: Prometheus + Grafana Observability

This step adds runtime observability to the Global Service Resiliency implementation.

## What changed

- Added `/metrics` endpoints to all Go services:
  - ingestor: `http://localhost:8081/metrics`
  - transformer: `http://localhost:8082/metrics`
  - aggregator: `http://localhost:8085/metrics`
  - healthcheck: `http://localhost:8083/metrics`
  - router: `http://localhost:8084/metrics`
  - api: `http://localhost:8080/metrics`
- Enabled Prometheus at `http://localhost:9090`
- Enabled Grafana at `http://localhost:3000`
- Added Grafana provisioning for:
  - Prometheus datasource
  - Global Service Resiliency Observability dashboard
- Added Prometheus metrics for:
  - records processed
  - service cycle duration
  - service errors
  - region latency
  - estimated replication lag
  - region health state
  - routing decisions
  - API request rate and duration

## Run commands

From the project root:

```bash
go mod tidy
go build ./...
docker compose down --remove-orphans
docker compose up --build
```

Or run in the background:

```bash
docker compose up -d --build
```

## Validate service metrics

```bash
curl http://localhost:8081/metrics | grep resiliency
curl http://localhost:8082/metrics | grep resiliency
curl http://localhost:8085/metrics | grep resiliency
curl http://localhost:8083/metrics | grep resiliency
curl http://localhost:8084/metrics | grep resiliency
curl http://localhost:8080/metrics | grep resiliency
```

## Validate Prometheus

Open:

```text
http://localhost:9090/targets
```

All Go services should show as `UP`.

Useful Prometheus queries:

```promql
sum by (service, record_type) (rate(resiliency_records_processed_total[5m])) * 60
sum by (service, error_type) (increase(resiliency_errors_total[15m]))
resiliency_region_latency_ms
resiliency_region_replication_lag_ms
resiliency_region_health_status
resiliency_routing_preferred_latency_ms
sum by (path, status_code) (rate(resiliency_api_requests_total[5m]))
```

## Validate Grafana

Open:

```text
http://localhost:3000
```

Login:

```text
username: admin
password: resiliency
```

Go to Dashboards and open:

```text
Global Service Resiliency Observability
```

## Interpretation note

The healthcheck service still uses local-development synthetic regional offsets on top of measured MongoDB ping latency. This is suitable for local demonstration, but it should be documented as an estimated regional health signal, not true production replication lag.

For production-grade measurement, replace the estimated `replication_lag_ms` with Atlas metrics API or replica-set optime comparison in a real multi-region MongoDB deployment.
