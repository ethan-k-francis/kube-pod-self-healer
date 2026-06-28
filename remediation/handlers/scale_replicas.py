"""
Scale Replicas Handler

Scales up a Deployment by adding one replica when pods are repeatedly failing.
This is an escalation strategy — if restarting pods doesn't fix the problem,
adding capacity might help (e.g., the failure is caused by resource contention
or traffic spikes exceeding the current pod count).

Safety guards:
    - Maximum replica limit prevents runaway scaling
    - Only scales Deployments (not DaemonSets, StatefulSets, etc.)
    - Logs every scaling action for audit trail

In production, you'd combine this with a Horizontal Pod Autoscaler (HPA) and
add a cool-down timer to prevent rapid successive scale-ups.
"""

import logging

from kubernetes import client, config

logger = logging.getLogger("remediation.scale_replicas")

# Hard ceiling on replicas. Prevents runaway scaling if something keeps
# failing regardless of replica count. In production, this would be
# configurable per-deployment.
MAX_REPLICAS = 10


def scale_replicas(
    deployment_name: str,
    namespace: str,
    scale_increment: int = 1,
) -> tuple[bool, str]:
    """
    Scale a Deployment up by the given increment to add capacity.

    Args:
        deployment_name: Name of the Deployment to scale
        namespace: Kubernetes namespace
        scale_increment: Number of replicas to add (default: 1)

    Returns:
        Tuple of (success, message) describing the outcome
    """
    if not deployment_name:
        return False, "no deployment name provided — cannot scale"

    deploy_key = f"{namespace}/{deployment_name}"

    try:
        # Load K8s config
        try:
            config.load_incluster_config()
        except config.ConfigException:
            config.load_kube_config()

        apps_v1 = client.AppsV1Api()

        # --- Read Current State ---
        # Get the current replica count so we can calculate the new target.
        # We always read-then-write to avoid race conditions (though for a
        # single remediation service, races are unlikely).
        logger.info("reading deployment %s", deploy_key)
        deployment = apps_v1.read_namespaced_deployment(
            name=deployment_name,
            namespace=namespace,
        )

        current_replicas = deployment.spec.replicas or 1
        desired_replicas = current_replicas + scale_increment

        # --- Safety Check ---
        # Don't scale beyond the maximum. This prevents a scenario where
        # a fundamental bug causes every replica to fail, and the remediation
        # service keeps adding replicas until the cluster runs out of resources.
        if desired_replicas > MAX_REPLICAS:
            msg = (
                f"scale limit reached for {deploy_key}: "
                f"current={current_replicas}, max={MAX_REPLICAS}"
            )
            logger.warning(msg)
            return False, msg

        # --- Scale Up ---
        # Patch the deployment's replica count. The Deployment controller
        # will create new pods to match the desired count.
        logger.info(
            "scaling %s: %d -> %d replicas",
            deploy_key,
            current_replicas,
            desired_replicas,
        )

        # Use a strategic merge patch — only modify the replica count,
        # leave everything else untouched
        patch_body = {"spec": {"replicas": desired_replicas}}
        apps_v1.patch_namespaced_deployment(
            name=deployment_name,
            namespace=namespace,
            body=patch_body,
        )

        msg = (
            f"scaled {deploy_key} from {current_replicas} to "
            f"{desired_replicas} replicas"
        )
        logger.info(msg)
        return True, msg

    except client.exceptions.ApiException as exc:
        if exc.status == 404:
            msg = f"deployment {deploy_key} not found (404)"
            logger.error(msg)
            return False, msg

        msg = f"K8s API error scaling {deploy_key}: {exc.status} {exc.reason}"
        logger.error(msg)
        return False, msg

    except Exception as exc:
        msg = f"unexpected error scaling {deploy_key}: {exc}"
        logger.exception(msg)
        return False, msg
