# Step 5 - Streamlit Dashboard

This step adds a dashboard layer that consumes the REST API Gateway instead of connecting directly to MongoDB.

## Dashboard reports covered

- RPT-01: GitHub Event Type Distribution
- RPT-03: Regional Weather Dashboard
- RPT-04: Region Health Monitor
- RPT-05: Routing Decision and Continuity Signal
- Executive summary: Combined API summary and data quality reminders

## Files added

```text
dashboards/streamlit/app.py
dashboards/streamlit/requirements.txt
deployments/docker/Dockerfile.dashboard
```

## Local manual run

Start the API Gateway first:

```bash
go run ./cmd/api
```

In a second terminal:

```bash
cd dashboards/streamlit
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
API_BASE_URL=http://localhost:8080 streamlit run app.py
```

Open the dashboard at:

```text
http://localhost:8501
```

## Docker Compose run

From the repo root:

```bash
docker compose up --build
```

Open the dashboard at:

```text
http://localhost:8501
```

Inside Docker Compose, the dashboard uses:

```text
API_BASE_URL=http://api:8080
```

For local manual runs, use:

```text
API_BASE_URL=http://localhost:8080
```

## Validation checklist

1. `/health` API endpoint returns healthy.
2. Dashboard shows GitHub total events and event type count.
3. Event type bar chart loads.
4. Regional weather charts load.
5. Region health and routing cards load.
6. Data quality reminders show at the bottom.

## Troubleshooting

If the dashboard cannot connect to the API:

```bash
curl http://localhost:8080/health
```

If Docker is running, check:

```bash
docker compose ps
docker compose logs api
docker compose logs dashboard
```

If API works locally but the Docker dashboard fails, confirm the dashboard container is using:

```text
API_BASE_URL=http://api:8080
```
