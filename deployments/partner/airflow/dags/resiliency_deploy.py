"""
resiliency_deploy.py

Deployment orchestration DAG — creates resources if they don't exist,
triggers a rolling restart if they do.

Deployment order:
  1. mongo        — StatefulSet  (skip if not found — needs mongo.yaml applied first)
  2. ingestors    — us-east + us-west in parallel
  3. healthcheck  — API layer
  4. eda          — CronJob suspend/resume
  5. smoke_test   — hits /readyz on the healthcheck service
"""
from datetime import datetime, timedelta, timezone
from airflow import DAG
from airflow.operators.python import PythonOperator
from kubernetes import client, config
from kubernetes.client.rest import ApiException
import time

default_args = {
    "owner": "resiliency-team",
    "retries": 1,
    "retry_delay": timedelta(minutes=2),
    "email_on_failure": False,
}

NAMESPACE = "resiliency"
GCR_PREFIX = "gcr.io/msds-432-g2-497302"


def _clients():
    config.load_incluster_config()
    return client.AppsV1Api(), client.BatchV1Api()


def _restart_patch():
    """Patch that triggers a rolling restart via annotation."""
    return {"spec": {"template": {"metadata": {"annotations": {
        "kubectl.kubernetes.io/restartedAt": datetime.now(timezone.utc).isoformat()
    }}}}}


def _ingestor_spec(region):
    name = f"ingestor-{region}"
    return client.V1Deployment(
        metadata=client.V1ObjectMeta(name=name, namespace=NAMESPACE, labels={"app": name}),
        spec=client.V1DeploymentSpec(
            replicas=1,
            selector=client.V1LabelSelector(match_labels={"app": name}),
            template=client.V1PodTemplateSpec(
                metadata=client.V1ObjectMeta(labels={"app": name}),
                spec=client.V1PodSpec(
                    containers=[client.V1Container(
                        name=name,
                        image=f"{GCR_PREFIX}/resiliency-ingestor:latest",
                        image_pull_policy="Always",
                        env=[client.V1EnvVar(name="REGION", value=region)],
                        env_from=[
                            client.V1EnvFromSource(config_map_ref=client.V1ConfigMapEnvSource(name="resiliency-config")),
                            client.V1EnvFromSource(secret_ref=client.V1SecretEnvSource(name="resiliency-secrets")),
                        ],
                        resources=client.V1ResourceRequirements(
                            requests={"cpu": "100m", "memory": "128Mi"},
                            limits={"cpu": "500m", "memory": "512Mi"},
                        ),
                        volume_mounts=[client.V1VolumeMount(name="data", mount_path="/app/data")],
                    )],
                    volumes=[client.V1Volume(name="data", empty_dir=client.V1EmptyDirVolumeSource())],
                )
            )
        )
    )


def _healthcheck_spec():
    return client.V1Deployment(
        metadata=client.V1ObjectMeta(name="healthcheck", namespace=NAMESPACE, labels={"app": "healthcheck"}),
        spec=client.V1DeploymentSpec(
            replicas=2,
            selector=client.V1LabelSelector(match_labels={"app": "healthcheck"}),
            template=client.V1PodTemplateSpec(
                metadata=client.V1ObjectMeta(labels={"app": "healthcheck"}),
                spec=client.V1PodSpec(
                    containers=[client.V1Container(
                        name="healthcheck",
                        image=f"{GCR_PREFIX}/resiliency-healthcheck:latest",
                        image_pull_policy="Always",
                        ports=[client.V1ContainerPort(container_port=8080)],
                        env_from=[
                            client.V1EnvFromSource(config_map_ref=client.V1ConfigMapEnvSource(name="resiliency-config")),
                            client.V1EnvFromSource(secret_ref=client.V1SecretEnvSource(name="resiliency-secrets")),
                        ],
                        resources=client.V1ResourceRequirements(
                            requests={"cpu": "50m", "memory": "64Mi"},
                            limits={"cpu": "200m", "memory": "128Mi"},
                        ),
                        liveness_probe=client.V1Probe(
                            http_get=client.V1HTTPGetAction(path="/livez", port=8080),
                            initial_delay_seconds=5, period_seconds=10,
                        ),
                        readiness_probe=client.V1Probe(
                            http_get=client.V1HTTPGetAction(path="/readyz", port=8080),
                            initial_delay_seconds=5, period_seconds=10,
                        ),
                    )],
                )
            )
        )
    )


def deploy_or_restart_deployment(name, spec_fn, **kwargs):
    """Create deployment if missing, otherwise trigger rolling restart."""
    apps_v1, _ = _clients()
    try:
        apps_v1.read_namespaced_deployment(name, NAMESPACE)
        print(f"Deployment '{name}' exists — triggering rolling restart")
        apps_v1.patch_namespaced_deployment(name, NAMESPACE, _restart_patch())
    except ApiException as e:
        if e.status == 404:
            print(f"Deployment '{name}' not found — creating it now")
            apps_v1.create_namespaced_deployment(NAMESPACE, spec_fn())
            print(f"Deployment '{name}' created")
        else:
            raise


def restart_statefulset(name, **kwargs):
    """Rolling restart StatefulSet. Skips if not found."""
    apps_v1, _ = _clients()
    try:
        apps_v1.read_namespaced_stateful_set(name, NAMESPACE)
        print(f"StatefulSet '{name}' exists — triggering rolling restart")
        apps_v1.patch_namespaced_stateful_set(name, NAMESPACE, _restart_patch())
    except ApiException as e:
        if e.status == 404:
            print(f"StatefulSet '{name}' not found — apply k8s/mongo.yaml first. Skipping.")
        else:
            raise


def bounce_cronjob(name, **kwargs):
    """Suspend then resume CronJob to force fresh image on next run."""
    _, batch_v1 = _clients()
    try:
        batch_v1.read_namespaced_cron_job(name, NAMESPACE)
        print(f"CronJob '{name}' found — suspending")
        batch_v1.patch_namespaced_cron_job(name, NAMESPACE, {"spec": {"suspend": True}})
        time.sleep(3)
        print(f"Resuming CronJob '{name}'")
        batch_v1.patch_namespaced_cron_job(name, NAMESPACE, {"spec": {"suspend": False}})
    except ApiException as e:
        if e.status == 404:
            print(f"CronJob '{name}' not found — skipping")
        else:
            raise


def wait_for_rollout(resource_type, resource_name, namespace=NAMESPACE, timeout=300, **kwargs):
    """Poll until replicas are ready. Skips gracefully if resource doesn't exist."""
    apps_v1, _ = _clients()
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if resource_type == "deployment":
                obj = apps_v1.read_namespaced_deployment(resource_name, namespace)
            else:
                obj = apps_v1.read_namespaced_stateful_set(resource_name, namespace)
            ready   = obj.status.ready_replicas or 0
            desired = obj.spec.replicas or 1
            if ready >= desired:
                print(f"{resource_name}: {ready}/{desired} ready")
                return
            print(f"{resource_name}: {ready}/{desired} ready, waiting...")
        except ApiException as e:
            if e.status == 404:
                print(f"SKIP: '{resource_name}' not found")
                return
            print(f"API error: {e}")
        time.sleep(10)
    raise TimeoutError(f"{resource_name} not ready within {timeout}s")


def smoke_test(**kwargs):
    """Verify /readyz on the healthcheck service."""
    import urllib.request
    url = "http://healthcheck.resiliency.svc.cluster.local:8080/readyz"
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            body = resp.read().decode()
            if resp.status == 200:
                print(f"Smoke test passed: {body}")
                return
            raise ValueError(f"Unexpected status {resp.status}: {body}")
    except Exception as e:
        raise RuntimeError(f"Smoke test failed — {url}: {e}")


with DAG(
    dag_id="resiliency_deploy",
    description="Create or rolling-restart all resiliency services in order",
    schedule=None,
    start_date=datetime(2026, 5, 30),
    catchup=False,
    default_args=default_args,
    tags=["resiliency", "deploy"],
) as dag:

    t_mongo = PythonOperator(
        task_id="deploy_mongo",
        python_callable=restart_statefulset,
        op_args=["mongo"],
    )
    t_wait_mongo = PythonOperator(
        task_id="wait_mongo",
        python_callable=wait_for_rollout,
        op_args=["statefulset", "mongo"],
    )
    t_ingestor_east = PythonOperator(
        task_id="deploy_ingestor_us_east",
        python_callable=deploy_or_restart_deployment,
        op_args=["ingestor-us-east", lambda: _ingestor_spec("us-east")],
    )
    t_ingestor_west = PythonOperator(
        task_id="deploy_ingestor_us_west",
        python_callable=deploy_or_restart_deployment,
        op_args=["ingestor-us-west", lambda: _ingestor_spec("us-west")],
    )
    t_wait_east = PythonOperator(
        task_id="wait_ingestor_us_east",
        python_callable=wait_for_rollout,
        op_args=["deployment", "ingestor-us-east"],
    )
    t_wait_west = PythonOperator(
        task_id="wait_ingestor_us_west",
        python_callable=wait_for_rollout,
        op_args=["deployment", "ingestor-us-west"],
    )
    t_healthcheck = PythonOperator(
        task_id="deploy_healthcheck",
        python_callable=deploy_or_restart_deployment,
        op_args=["healthcheck", _healthcheck_spec],
    )
    t_wait_health = PythonOperator(
        task_id="wait_healthcheck",
        python_callable=wait_for_rollout,
        op_args=["deployment", "healthcheck"],
    )
    t_eda = PythonOperator(
        task_id="bounce_eda_cronjob",
        python_callable=bounce_cronjob,
        op_args=["eda"],
    )
    t_smoke = PythonOperator(
        task_id="smoke_test",
        python_callable=smoke_test,
    )

    t_mongo >> t_wait_mongo
    t_wait_mongo >> [t_ingestor_east, t_ingestor_west]
    t_ingestor_east >> t_wait_east
    t_ingestor_west >> t_wait_west
    [t_wait_east, t_wait_west] >> t_healthcheck >> t_wait_health
    t_wait_health >> t_eda >> t_smoke
