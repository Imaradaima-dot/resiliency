"""
resiliency_pipeline.py

Orchestrates the Global Service Resiliency data pipeline on GKE.

Schedule: every 30 minutes
  1. ingest_us_east   — runs resiliency-ingestor for us-east region
  2. ingest_us_west   — runs resiliency-ingestor for us-west region  (parallel)
  3. ingest_europe    — runs resiliency-ingestor for europe region    (parallel)
  4. run_eda          — runs resiliency-eda after all ingestors finish

Each task uses KubernetesPodOperator so it spins up an isolated pod,
uses the same images already deployed by k8s/deployment.yaml, and
inherits the resiliency-config ConfigMap + resiliency-secrets Secret.
"""

from datetime import datetime, timedelta

from airflow import DAG
from airflow.providers.cncf.kubernetes.operators.pod import KubernetesPodOperator
from kubernetes.client import models as k8s

default_args = {
    "owner": "resiliency-team",
    "retries": 2,
    "retry_delay": timedelta(minutes=5),
    "email_on_failure": False,
}

# Shared env sources — mirrors deployment.yaml envFrom blocks
_env_from = [
    k8s.V1EnvFromSource(
        config_map_ref=k8s.V1ConfigMapEnvSource(name="resiliency-config")
    ),
    k8s.V1EnvFromSource(
        secret_ref=k8s.V1SecretEnvSource(name="resiliency-secrets")
    ),
]

# Shared resource spec for ingestor pods
_ingestor_resources = k8s.V1ResourceRequirements(
    requests={"cpu": "100m", "memory": "128Mi"},
    limits={"cpu": "500m", "memory": "512Mi"},
)

# Shared resource spec for EDA pod
_eda_resources = k8s.V1ResourceRequirements(
    requests={"cpu": "200m", "memory": "256Mi"},
    limits={"cpu": "1000m", "memory": "1Gi"},
)

with DAG(
    dag_id="resiliency_pipeline",
    description="Ingest weather data for all regions then run EDA",
    schedule="*/30 * * * *",
    start_date=datetime(2026, 5, 30),
    catchup=False,
    default_args=default_args,
    tags=["resiliency", "ingestion", "eda"],
) as dag:

    def make_ingestor_task(region: str) -> KubernetesPodOperator:
        return KubernetesPodOperator(
            task_id=f"ingest_{region.replace('-', '_')}",
            name=f"ingestor-{region}-{{{{ ds_nodash }}}}",
            namespace="resiliency",
            image="gcr.io/msds-432-g2-497302/resiliency-ingestor:latest",
            image_pull_policy="Always",
            env_from=_env_from,
            env_vars=[
                k8s.V1EnvVar(name="REGION", value=region),
            ],
            container_resources=_ingestor_resources,
            is_delete_operator_pod=True,
            get_logs=True,
            in_cluster=True,
        )

    ingest_us_east = make_ingestor_task("us-east")
    ingest_us_west = make_ingestor_task("us-west")
    ingest_europe  = make_ingestor_task("europe")

    run_eda = KubernetesPodOperator(
        task_id="run_eda",
        name="eda-{{ ds_nodash }}",
        namespace="resiliency",
        image="gcr.io/msds-432-g2-497302/resiliency-eda:latest",
        image_pull_policy="Always",
        env_from=_env_from,
        container_resources=_eda_resources,
        is_delete_operator_pod=True,
        get_logs=True,
        in_cluster=True,
    )

    # ingestors run in parallel; EDA waits for all three
    [ingest_us_east, ingest_us_west, ingest_europe] >> run_eda
