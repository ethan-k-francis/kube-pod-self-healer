# Infra Autopilot — Operational Runbook

This runbook covers deployment, operation, extension, and troubleshooting of the
Infra Autopilot self-healing system.

---

## Table of Contents

1. [Deploying the System](#deploying-the-system)
2. [Adding New Remediation Handlers](#adding-new-remediation-handlers)
3. [Configuring Alert Webhooks](#configuring-alert-webhooks)
4. [Monitoring and Observability](#monitoring-and-observability)
5. [Troubleshooting](#troubleshooting)

---

## Deploying the System

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (for Kind and image builds)
- [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/) (Kubernetes IN Docker)
- [kubectl](https://kubernetes.io/docs/tasks/tools/) (Kubernetes CLI)
- [Go 1.23+](https://go.dev/dl/) (for building the agent)
- [Python 3.12+](https://python.org) (for the remediation service)

### Step 1: Create the Cluster

```bash
make cluster-up
```

This creates a Kind cluster named `autopilot` with 1 control-plane node and
2 worker nodes. Port mappings (30080, 30443) are configured for NodePort access.

### Step 2: Build and Deploy

```bash
make deploy
```

This builds both container images, loads them into Kind, and applies all
Kubernetes manifests (namespace, RBAC, agent deployment, remediation deployment).

### Step 3: Verify

```bash
make status
```

You should see the `health-agent` and `remediation-service` pods in `Running` state.

### Step 4: Demo

```bash
make demo
```

This deploys deliberately broken workloads (CrashLoopBackOff, probe failures)
and tails the agent logs so you can watch the detection and remediation in real time.

### Teardown

```bash
make cluster-down
```

---

## Adding New Remediation Handlers

Handlers live in `remediation/handlers/`. Each handler is a Python module that
implements a single function.

### Handler Interface

Every handler function must:
1. Accept the parameters it needs (pod name, namespace, etc.)
2. Return a tuple of `(success: bool, message: str)`
3. Handle exceptions internally (never let exceptions propagate)

### Step-by-Step

1. **Create the handler file:**

```python
# remediation/handlers/drain_node.py
"""
Drain Node Handler
Cordons and drains the node when multiple pods on the same node are failing.
"""
import logging
from kubernetes import client, config

logger = logging.getLogger("remediation.drain_node")

def drain_node(node_name: str) -> tuple[bool, str]:
    try:
        config.load_incluster_config()
    except config.ConfigException:
        config.load_kube_config()

    v1 = client.CoreV1Api()
    # Implementation here...
    return True, f"node {node_name} drained"
```

2. **Register it in `handlers/__init__.py`:**

```python
from handlers.drain_node import drain_node
__all__ = [..., "drain_node"]
```

3. **Add routing in `remediation_server.py`:**

```python
REMEDIATION_MAP["NodeFailure"] = "drain_node"
```

4. **Add the dispatch in `_execute_action()`:**

```python
elif action == "drain_node":
    success, message = drain_node(node_name=event.node_name or "")
```

5. **Add the corresponding failure type in the Go agent** (`agent/internal/watcher/events.go`):

```go
FailureNodeFailure FailureType = "NodeFailure"
```

---

## Configuring Alert Webhooks

The health agent sends JSON POST requests to a configurable webhook URL for both
detection and remediation events.

### Slack

1. Create a [Slack Incoming Webhook](https://api.slack.com/messaging/webhooks)
2. Set the `WEBHOOK_URL` environment variable:

```yaml
# In deploy/manifests/agent-deployment.yaml
env:
  - name: WEBHOOK_URL
    value: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXX"
```

The agent sends a `text` field in the payload, which Slack renders as the message body.

### Discord

1. Create a Discord webhook in channel settings
2. Append `/slack` to the webhook URL for Slack-compatible format:

```yaml
env:
  - name: WEBHOOK_URL
    value: "https://discord.com/api/webhooks/000000/XXXX/slack"
```

### Custom Endpoint

Any HTTP endpoint that accepts `POST` with `Content-Type: application/json` will work.
The payload includes:

```json
{
  "text": "[Autopilot] Detected CrashLoopBackOff: pod autopilot/my-pod...",
  "event_id": "my-pod-main-1708000000000",
  "failure_type": "CrashLoopBackOff",
  "pod_name": "my-pod-abc123",
  "namespace": "autopilot",
  "message": "container \"main\" in CrashLoopBackOff (restarts: 5)",
  "timestamp": "2026-02-22T12:00:00Z",
  "remediation_action": "restart",
  "remediation_success": true,
  "remediation_message": "successfully deleted pod autopilot/my-pod-abc123"
}
```

---

## Monitoring and Observability

### Agent Logs

```bash
make agent-logs          # Tail health agent logs
make remediation-logs    # Tail remediation service logs
```

### Remediation History

The remediation service exposes recent actions at `GET /history`:

```bash
kubectl port-forward -n autopilot svc/remediation-service 8000:8000
curl http://localhost:8000/history | python -m json.tool
```

### Pod Status

```bash
make status    # Full overview: pods, services, events
```

### Key Log Messages

| Log | Meaning |
|---|---|
| `[watcher] detected CrashLoopBackOff` | Agent found a crashlooping pod |
| `[remediation-client] sending event` | Agent is forwarding to remediation |
| `[remediation-client] result: action=restart success=true` | Remediation succeeded |
| `[webhook] notification sent` | Alert delivered to webhook |
| `[watcher] cache synced, watching` | Agent is healthy and watching |

---

## Troubleshooting

### Agent pod is CrashLoopBackOff

**Cause:** Usually a misconfigured RBAC or the remediation service isn't reachable.

```bash
kubectl logs -n autopilot -l app=health-agent --previous
kubectl describe pod -n autopilot -l app=health-agent
```

Check:
- ServiceAccount exists: `kubectl get sa -n autopilot health-agent`
- ClusterRoleBinding exists: `kubectl get clusterrolebinding health-agent`
- Remediation service is up: `kubectl get pod -n autopilot -l app=remediation-service`

### Remediation service returns 422

**Cause:** The event payload doesn't match the Pydantic model. Check the Go agent's event JSON format matches the Python model fields.

```bash
kubectl logs -n autopilot -l app=remediation-service
```

### Events not being detected

**Cause:** Informer cache hasn't synced, or the namespace filter is wrong.

Check:
- Agent logs for "cache synced" message
- `NAMESPACE` env var matches where failing pods run
- Agent has `get/list/watch` permissions on pods

### Remediation not taking effect

**Cause:** RBAC permissions might be insufficient, or the remediation service can't reach the K8s API.

```bash
# Test RBAC manually
kubectl auth can-i delete pods -n autopilot --as=system:serviceaccount:autopilot:health-agent
kubectl auth can-i patch deployments -n autopilot --as=system:serviceaccount:autopilot:health-agent
```

### Images not loading into Kind

**Cause:** Image name mismatch between `docker build` and `kind load`.

```bash
docker images | grep infra-autopilot
kind load docker-image infra-autopilot/agent:latest --name autopilot
```

The image tag in the Deployment manifest must exactly match the loaded image name.
`imagePullPolicy: Never` is required to prevent Kind from trying to pull from a registry.

### Webhook notifications not arriving

Check:
- `WEBHOOK_URL` is set in the agent deployment
- The URL is reachable from inside the cluster (pod DNS resolution works for external URLs)
- Agent logs show `[webhook] notification sent` or error messages

```bash
# Test webhook connectivity from inside the cluster
kubectl run -n autopilot test-curl --rm -i --restart=Never --image=curlimages/curl -- \
  curl -s -o /dev/null -w "%{http_code}" https://hooks.slack.com/services/YOUR/WEBHOOK/URL
```
