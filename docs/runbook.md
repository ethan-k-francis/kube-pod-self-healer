# Kube Pod Self-Healer — Operator Guide

Step-by-step instructions for deploying, extending, and troubleshooting the self-healing system.

**New to Kubernetes (K8s)?** Read the [main README](../README.md) first for the big picture. This guide assumes you're running the local Kind (Kubernetes IN Docker) cluster from the Makefile.

---

## Table of Contents

1. [Deploying the System](#deploying-the-system)
2. [Adding New Fix Handlers](#adding-new-fix-handlers)
3. [Setting Up Alert Notifications](#setting-up-alert-notifications)
4. [Checking Logs and History](#checking-logs-and-history)
5. [Troubleshooting](#troubleshooting)

---

## Deploying the System

### What you need installed

| Tool | Purpose |
|---|---|
| [Docker](https://docs.docker.com/get-docker/) | Runs Kind and builds container images |
| [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/) | Local Kubernetes (K8s) cluster inside Docker |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | Command-line tool to talk to Kubernetes (K8s) |
| [Go 1.23+](https://go.dev/dl/) | Build the health agent |
| [Python 3.12+](https://python.org) | Run the remediation service locally (optional) |

### Step 1: Create the cluster

```bash
make cluster-up
```

Creates a cluster named `kube-pod-self-healer` with 1 control-plane node and 2 worker nodes.

### Step 2: Build and deploy

```bash
make deploy
```

Builds both container images, loads them into Kind, and applies Kubernetes (K8s) manifests (namespace, Role-Based Access Control (RBAC) permissions, agent, remediation service).

### Step 3: Verify

```bash
make status
```

Both `health-agent` and `remediation-service` pods should show `Running`.

### Step 4: Run the demo

```bash
make demo
```

Deploys intentionally broken workloads and tails agent logs so you can watch detection and auto-fix in real time.

### Teardown

```bash
make cluster-down
```

---

## Adding New Fix Handlers

Handlers live in `remediation/handlers/`. Each handler is one Python function that performs a single fix action.

### Handler contract

Every handler must:

1. Accept the parameters it needs (pod name, namespace, etc.)
2. Return `(success: bool, message: str)`
3. Catch its own exceptions — never crash the server

### Example: add a "drain node" handler

**1. Create the handler file:**

```python
# remediation/handlers/drain_node.py
"""Cordons and drains a node when multiple pods on it are failing."""
import logging
from kubernetes import client, config

logger = logging.getLogger("remediation.drain_node")

def drain_node(node_name: str) -> tuple[bool, str]:
    try:
        config.load_incluster_config()
    except config.ConfigException:
        config.load_kube_config()

    v1 = client.CoreV1Api()
    # ... implementation ...
    return True, f"node {node_name} drained"
```

**2. Register in `handlers/__init__.py`:**

```python
from handlers.drain_node import drain_node
__all__ = [..., "drain_node"]
```

**3. Map failure type to handler in `remediation_server.py`:**

```python
REMEDIATION_MAP["NodeFailure"] = "drain_node"
```

**4. Add dispatch in `_execute_action()`:**

```python
elif action == "drain_node":
    success, message = drain_node(node_name=event.node_name or "")
```

**5. Add the failure type in the Go agent** (`agent/internal/watcher/events.go`):

```go
FailureNodeFailure FailureType = "NodeFailure"
```

---

## Setting Up Alert Notifications

The health agent sends JavaScript Object Notation (JSON) Hypertext Transfer Protocol (HTTP) POST requests to a webhook URL when it detects failures and when fixes complete.

### Slack

1. Create a [Slack Incoming Webhook](https://api.slack.com/messaging/webhooks)
2. Set `WEBHOOK_URL` in the agent deployment:

```yaml
# deploy/manifests/agent-deployment.yaml
env:
  - name: WEBHOOK_URL
    value: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXX"
```

Slack reads the `text` field from the JSON (JavaScript Object Notation) body.

### Discord

1. Create a webhook in your Discord channel settings
2. Append `/slack` to the URL for Slack-compatible format:

```yaml
env:
  - name: WEBHOOK_URL
    value: "https://discord.com/api/webhooks/000000/XXXX/slack"
```

### Custom endpoint

Any URL that accepts `POST` with JavaScript Object Notation (JSON) works. Example payload:

```json
{
  "text": "[KubePodSelfHealer] Detected CrashLoopBackOff: pod kube-pod-self-healer/my-pod...",
  "event_id": "my-pod-main-1708000000000",
  "failure_type": "CrashLoopBackOff",
  "pod_name": "my-pod-abc123",
  "namespace": "kube-pod-self-healer",
  "message": "container \"main\" in CrashLoopBackOff (restarts: 5)",
  "timestamp": "2026-02-22T12:00:00Z",
  "remediation_action": "restart",
  "remediation_success": true,
  "remediation_message": "successfully deleted pod kube-pod-self-healer/my-pod-abc123"
}
```

---

## Checking Logs and History

### Live logs

```bash
make agent-logs              # Last 100 lines from health agent
make remediation-logs        # Last 100 lines from remediation service
make agent-logs-follow       # Stream health agent (Ctrl+C to stop)
make remediation-logs-follow # Stream remediation service (Ctrl+C to stop)
```

`make remediation-logs` used to run `kubectl logs -f`, which exits with Make error 1 when you press Ctrl+C — that is normal, not a service failure.

### Recent fix history

The remediation service exposes past actions at `GET /history`:

```bash
kubectl port-forward -n kube-pod-self-healer svc/remediation-service 8000:8000
curl http://localhost:8000/history | python -m json.tool
```

### Pod overview

```bash
make status
```

### Log messages to look for

| Log line | Meaning |
|---|---|
| `[watcher] detected CrashLoopBackOff` | Agent found a crash-looping pod |
| `[remediation-client] sending event` | Agent forwarded event to fix service |
| `[remediation-client] result: action=restart success=true` | Fix succeeded |
| `[webhook] notification sent` | Alert delivered |
| `[watcher] cache synced, watching` | Agent is healthy and watching |

---

## Troubleshooting

### Agent pod keeps crashing (CrashLoopBackOff)

**Likely cause:** Wrong Role-Based Access Control (RBAC) permissions or remediation service unreachable.

```bash
kubectl logs -n kube-pod-self-healer -l app=health-agent --previous
kubectl describe pod -n kube-pod-self-healer -l app=health-agent
```

Check:
- ServiceAccount exists: `kubectl get sa -n kube-pod-self-healer health-agent`
- ClusterRoleBinding exists: `kubectl get clusterrolebinding health-agent`
- Remediation service is running: `kubectl get pod -n kube-pod-self-healer -l app=remediation-service`

### Remediation service returns 422

**Likely cause:** JSON from the Go agent doesn't match the Python event model (Pydantic validation).

```bash
kubectl logs -n kube-pod-self-healer -l app=remediation-service
```

Compare field names in the agent's event struct vs. the Pydantic model in the remediation service.

### Failures not being detected

**Likely cause:** Agent hasn't finished syncing its cache, or namespace filter is wrong.

Check:
- Agent logs contain "cache synced"
- `NAMESPACE` env var matches where failing pods run
- Agent has `get/list/watch` on pods

### Fix runs but nothing changes

**Likely cause:** Insufficient Role-Based Access Control (RBAC) permissions, or the remediation service can't reach the Kubernetes (K8s) API.

```bash
kubectl auth can-i delete pods -n kube-pod-self-healer --as=system:serviceaccount:kube-pod-self-healer:health-agent
kubectl auth can-i patch deployments -n kube-pod-self-healer --as=system:serviceaccount:kube-pod-self-healer:health-agent
```

### Images not loading into Kind

**Likely cause:** Image name in `docker build` doesn't match what's in the Deployment manifest.

```bash
docker images | grep kube-pod-self-healer
kind load docker-image kube-pod-self-healer/agent:latest --name kube-pod-self-healer
```

The Deployment must use `imagePullPolicy: Never` so Kind uses the locally loaded image instead of pulling from a registry.

### Webhook notifications not arriving

Check:
- `WEBHOOK_URL` is set in the agent deployment
- URL is reachable from inside the cluster
- Agent logs show `[webhook] notification sent` or an error

```bash
kubectl run -n kube-pod-self-healer test-curl --rm -i --restart=Never --image=curlimages/curl -- \
  curl -s -o /dev/null -w "%{http_code}" https://hooks.slack.com/services/YOUR/WEBHOOK/URL
```
