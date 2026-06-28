"""
Clear Cache Handler

Attempts to clear cached state inside the container by exec-ing a command.
This is useful for pods that become unhealthy due to corrupted caches, stale
connections, or accumulated temporary files.

The handler tries the in-container approach first (non-disruptive), and falls
back to a full pod restart if exec fails (e.g., the container is in a crash
state and can't accept exec connections).

Real-world applications:
    - Clear Redis/Memcached temp files
    - Remove stale PID files that prevent process startup
    - Reset local state directories
    - Flush DNS resolver caches
"""

import logging

from kubernetes import client, config
from kubernetes.stream import stream

logger = logging.getLogger("remediation.clear_cache")

# Default command to run inside the container. In a production system, this
# would be configurable per-deployment via annotations or a config map.
DEFAULT_CACHE_CLEAR_CMD = [
    "/bin/sh", "-c",
    "rm -rf /tmp/cache/* 2>/dev/null; echo 'cache cleared'"
]


def clear_cache(
    pod_name: str,
    namespace: str,
    container_name: str,
) -> tuple[bool, str]:
    """
    Exec into the pod to clear cached state. Falls back to pod deletion
    if the exec fails.
    
    Args:
        pod_name: Name of the target pod
        namespace: Kubernetes namespace
        container_name: Specific container to exec into
    
    Returns:
        Tuple of (success, message) describing the outcome
    """
    try:
        # Load K8s config — same pattern as other handlers
        try:
            config.load_incluster_config()
        except config.ConfigException:
            config.load_kube_config()

        v1 = client.CoreV1Api()
        pod_key = f"{namespace}/{pod_name}"

        # --- Try Exec Approach ---
        # Use the Kubernetes exec API to run a command inside the running
        # container. This is the equivalent of `kubectl exec`. The stream()
        # function handles the WebSocket upgrade that the exec API requires.
        logger.info(
            "attempting cache clear via exec: pod=%s container=%s",
            pod_key, container_name,
        )

        try:
            exec_output = stream(
                v1.connect_get_namespaced_pod_exec,
                pod_name,
                namespace,
                container=container_name,
                command=DEFAULT_CACHE_CLEAR_CMD,
                stderr=True,
                stdin=False,
                stdout=True,
                tty=False,
                # 10-second timeout — if the exec doesn't complete quickly,
                # the container is probably in a bad state
                _request_timeout=10,
            )

            msg = f"cache cleared in {pod_key}/{container_name}: {exec_output.strip()}"
            logger.info(msg)
            return True, msg

        except Exception as exec_err:
            # Exec failed — the container might be crashed, restarting, or
            # the image might not have /bin/sh. Fall back to pod deletion.
            logger.warning(
                "exec failed for %s/%s, falling back to pod restart: %s",
                pod_key, container_name, exec_err,
            )
            return _fallback_restart(v1, pod_name, namespace)

    except Exception as exc:
        msg = f"unexpected error in clear_cache for {namespace}/{pod_name}: {exc}"
        logger.exception(msg)
        return False, msg


def _fallback_restart(
    v1: client.CoreV1Api,
    pod_name: str,
    namespace: str,
) -> tuple[bool, str]:
    """
    Fallback: delete the pod to force a clean restart. Used when exec-based
    cache clearing fails (container not running, no shell available, etc.)
    """
    pod_key = f"{namespace}/{pod_name}"
    try:
        logger.info("fallback: deleting pod %s", pod_key)
        v1.delete_namespaced_pod(
            name=pod_name,
            namespace=namespace,
            body=client.V1DeleteOptions(grace_period_seconds=10),
        )
        msg = f"cache clear failed, pod {pod_key} deleted as fallback"
        logger.info(msg)
        return True, msg

    except client.exceptions.ApiException as exc:
        if exc.status == 404:
            return True, f"pod {pod_key} already gone (404)"

        msg = f"fallback restart failed for {pod_key}: {exc.status} {exc.reason}"
        logger.error(msg)
        return False, msg
