"""
Restart Pod Handler

Deletes the failing pod, which triggers the Deployment's ReplicaSet controller
to create a fresh replacement. This is the simplest remediation — it works for
transient failures like process crashes, temporary resource exhaustion, and
corrupted in-memory state.

Why delete instead of using `kubectl rollout restart`?
    Deleting a specific pod is more surgical. Rollout restart recreates ALL pods
    in the Deployment, which is overkill when only one pod is misbehaving. The
    ReplicaSet controller ensures the desired replica count is maintained, so
    deleting one pod immediately creates a new one.

Backoff logic:
    If a pod has restarted many times, we add a grace period before deleting it
    again. This prevents the remediation service from fighting Kubernetes' own
    backoff (which increases the wait between restarts exponentially).
"""

import logging
import time

from kubernetes import client, config

logger = logging.getLogger("remediation.restart_pod")

# Track recent restarts to implement backoff. Maps "namespace/pod" -> timestamp.
# Prevents the remediation service from deleting the same pod too frequently.
_recent_restarts: dict[str, float] = {}

# Minimum seconds between restart attempts for the same pod
RESTART_COOLDOWN_SECONDS = 60


def restart_pod(
    pod_name: str,
    namespace: str,
    restart_count: int = 0,
) -> tuple[bool, str]:
    """
    Delete a pod to trigger a fresh restart by the ReplicaSet controller.
    
    Args:
        pod_name: Name of the pod to restart
        namespace: Kubernetes namespace
        restart_count: Current restart count (used for backoff decisions)
    
    Returns:
        Tuple of (success, message) describing the outcome
    """
    pod_key = f"{namespace}/{pod_name}"

    # --- Backoff Check ---
    # If we recently restarted this pod, skip to avoid thrashing. Kubernetes
    # has its own exponential backoff for CrashLoopBackOff, and we don't want
    # to interfere with it by deleting the pod during the backoff window.
    last_restart = _recent_restarts.get(pod_key)
    if last_restart is not None:
        elapsed = time.time() - last_restart
        if elapsed < RESTART_COOLDOWN_SECONDS:
            remaining = int(RESTART_COOLDOWN_SECONDS - elapsed)
            msg = f"skipping restart for {pod_key}: cooldown active ({remaining}s remaining)"
            logger.info(msg)
            return True, msg

    try:
        # Load K8s config — in-cluster when running as a pod, kubeconfig when local
        try:
            config.load_incluster_config()
        except config.ConfigException:
            config.load_kube_config()

        v1 = client.CoreV1Api()

        # Delete the pod. The default grace period (30s) gives the container
        # time to handle SIGTERM and shut down cleanly. For crashlooping pods,
        # this grace period is usually irrelevant since the process is already dead.
        logger.info("deleting pod %s in namespace %s (restart_count=%d)", pod_name, namespace, restart_count)
        v1.delete_namespaced_pod(
            name=pod_name,
            namespace=namespace,
            body=client.V1DeleteOptions(
                grace_period_seconds=10,
            ),
        )

        # Record the restart time for backoff tracking
        _recent_restarts[pod_key] = time.time()

        msg = f"successfully deleted pod {pod_key} (restart #{restart_count + 1})"
        logger.info(msg)
        return True, msg

    except client.exceptions.ApiException as exc:
        # 404 means the pod was already deleted (maybe by another controller
        # or by Kubernetes itself). This is not an error for us.
        if exc.status == 404:
            msg = f"pod {pod_key} already deleted (404)"
            logger.info(msg)
            return True, msg

        msg = f"K8s API error deleting pod {pod_key}: {exc.status} {exc.reason}"
        logger.error(msg)
        return False, msg

    except Exception as exc:
        msg = f"unexpected error deleting pod {pod_key}: {exc}"
        logger.exception(msg)
        return False, msg
