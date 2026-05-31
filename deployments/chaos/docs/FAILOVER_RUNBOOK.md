# Failover and Continuity Runbook

## Objective

Validate that the Global Service Resiliency implementation can respond to regional health changes and maintain a valid routing decision.

## Test 1 - Application-level routing failover

The script:

1. Reads the current routing decision.
2. Captures the current preferred region.
3. Scales the healthcheck deployment to zero so it does not overwrite the test condition.
4. Marks the preferred region as `down` in MongoDB Atlas serving collection.
5. Calls the router refresh endpoint.
6. Confirms the preferred region changes to a healthy fallback.
7. Restores the healthcheck deployment.

Expected result:

```text
PASS: router avoided the failed preferred region
```

## Test 2 - Kubernetes API pod recovery

The script:

1. Confirms the API is healthy.
2. Deletes one API pod.
3. Polls the API health endpoint until it is healthy again.
4. Records approximate recovery time in seconds.

Expected result:

```text
PASS: API recovered
```

## Evidence to collect for documentation

| Evidence | File |
|---|---|
| Baseline routing decision | `baseline_routing.json` |
| Baseline API summary | `baseline_api_summary.json` |
| Failover before decision | `failover_before_routing.json` |
| Failover after decision | `failover_after_routing.json` |
| Failover result | `failover_result.txt` |
| API recovery result | `api_recovery_result.txt` |
| Final pod status | `final_pods.txt` |

## Limitations

This is a controlled GKE validation, not full chaos engineering. A full production-grade test would add:

- Multi-cluster deployment
- Cloud load balancer failover
- Network partition simulation
- Node pool disruption
- Real MongoDB Atlas replication lag metrics
- Automated pass/fail reporting in CI/CD
