"""
Kube Pod Self-Healer — Remediation Service

FastAPI server that receives pod failure events from the Go health agent and
routes them to the appropriate remediation handler. Each handler implements a
specific remediation strategy (restart, scale, cache-clear).

Architecture:
    Go Agent -> POST /remediate -> Router -> Handler -> K8s API

The service also exposes:
    GET /health  — liveness/readiness probe endpoint
    GET /history — recent remediation actions for observability
"""

import logging
import os
from collections import deque
from datetime import datetime, timezone
from typing import Optional

from fastapi import FastAPI
from pydantic import BaseModel, Field

from handlers.restart_pod import restart_pod
from handlers.clear_cache import clear_cache
from handlers.scale_replicas import scale_replicas

# ---------------------------------------------------------------------------
# Logging Configuration
# Use structured log format so logs are parseable by observability tools.
# ---------------------------------------------------------------------------
logging.basicConfig(
    level=getattr(logging, os.getenv("LOG_LEVEL", "INFO").upper(), logging.INFO),
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger("remediation")

# ---------------------------------------------------------------------------
# FastAPI Application
# ---------------------------------------------------------------------------
app = FastAPI(
    title="Kube Pod Self-Healer Remediation Service",
    description="Receives pod failure events and executes automated remediation",
    version="1.0.0",
)

# ---------------------------------------------------------------------------
# Data Models
#
# Pydantic models validate incoming request payloads and provide automatic
# OpenAPI documentation. If the Go agent sends a malformed event, FastAPI
# returns a 422 with details about what's wrong.
# ---------------------------------------------------------------------------


class RemediationEvent(BaseModel):
    """Incoming event from the Go health agent describing a pod failure."""

    event_id: str = Field(..., description="Unique event ID for tracking")
    timestamp: str = Field(..., description="ISO 8601 timestamp of detection")
    failure_type: str = Field(
        ...,
        description="Type: CrashLoopBackOff, OOMKilled, ProbeFailure, ImagePullBackOff",
    )
    pod_name: str = Field(..., description="Name of the failing pod")
    namespace: str = Field(..., description="Kubernetes namespace")
    owner_name: Optional[str] = Field(None, description="Deployment or controller name")
    owner_kind: Optional[str] = Field(None, description="Kind of the owner resource")
    container_name: str = Field(..., description="Failed container name")
    restart_count: int = Field(0, description="Container restart count")
    message: str = Field("", description="Human-readable failure description")
    node_name: Optional[str] = Field(None, description="Node running the pod")


class RemediationResult(BaseModel):
    """Result of a remediation action, returned to the caller and stored in history."""

    event_id: str
    action: str
    success: bool
    message: str
    timestamp: str


# ---------------------------------------------------------------------------
# History Buffer
#
# In-memory ring buffer of recent remediation actions. Capped at 100 entries
# to prevent unbounded memory growth. For production, you'd persist this to
# a database or push to a metrics system.
# ---------------------------------------------------------------------------
MAX_HISTORY = 100
history: deque[dict] = deque(maxlen=MAX_HISTORY)

# ---------------------------------------------------------------------------
# Remediation Router
#
# Maps failure types to handler functions. This is the core routing logic —
# when a new failure type is added, you just add a new entry here and write
# the handler. The handler interface is simple: take the event, return a
# RemediationResult.
# ---------------------------------------------------------------------------
REMEDIATION_MAP: dict[str, str] = {
    "CrashLoopBackOff": "restart",
    "OOMKilled": "restart",
    "ProbeFailure": "clear_cache",
    "ImagePullBackOff": "skip",
}

# Scale threshold — if a pod has restarted more than this many times, we
# escalate from simple restart to scaling up the deployment.
SCALE_THRESHOLD = 5


@app.post("/remediate", response_model=RemediationResult)
async def remediate(event: RemediationEvent) -> RemediationResult:
    """
    Main remediation endpoint. Receives a failure event, determines the
    appropriate action, executes it, and returns the result.

    Remediation strategy:
    - CrashLoopBackOff with low restarts -> restart pod
    - CrashLoopBackOff with high restarts -> scale up deployment
    - OOMKilled -> restart pod (with higher restart threshold for scaling)
    - ProbeFailure -> clear cache, then restart
    - ImagePullBackOff -> skip (can't fix wrong image names automatically)
    """
    logger.info(
        "received event: type=%s pod=%s/%s restarts=%d",
        event.failure_type,
        event.namespace,
        event.pod_name,
        event.restart_count,
    )

    action = REMEDIATION_MAP.get(event.failure_type, "skip")

    # Escalate to scaling if restarts exceed the threshold. This prevents
    # infinite restart loops — if restarting doesn't fix it, adding capacity
    # might help (e.g., resource contention).
    if action == "restart" and event.restart_count > SCALE_THRESHOLD:
        if event.owner_name and event.owner_kind == "Deployment":
            action = "scale"
            logger.info(
                "escalating to scale: restart_count=%d > threshold=%d",
                event.restart_count,
                SCALE_THRESHOLD,
            )

    # Execute the remediation action
    result = _execute_action(action, event)

    # Store in history for the /history endpoint
    history.append(result.model_dump())

    return result


def _execute_action(action: str, event: RemediationEvent) -> RemediationResult:
    """
    Dispatch to the appropriate handler based on the action type.
    Each handler is a standalone module in the handlers/ package.
    """
    timestamp = datetime.now(timezone.utc).isoformat()

    try:
        if action == "restart":
            success, message = restart_pod(
                pod_name=event.pod_name,
                namespace=event.namespace,
                restart_count=event.restart_count,
            )
        elif action == "clear_cache":
            success, message = clear_cache(
                pod_name=event.pod_name,
                namespace=event.namespace,
                container_name=event.container_name,
            )
        elif action == "scale":
            success, message = scale_replicas(
                deployment_name=event.owner_name or "",
                namespace=event.namespace,
            )
        elif action == "skip":
            success = True
            message = f"skipped remediation for {event.failure_type} (no automated fix)"
            logger.info("skipping: %s", message)
        else:
            success = False
            message = f"unknown action: {action}"
            logger.warning("unknown action: %s", action)

    except Exception as exc:
        success = False
        message = f"handler exception: {exc}"
        logger.exception(
            "remediation failed for %s/%s", event.namespace, event.pod_name
        )

    return RemediationResult(
        event_id=event.event_id,
        action=action,
        success=success,
        message=message,
        timestamp=timestamp,
    )


@app.get("/health")
async def health() -> dict:
    """
    Health check endpoint for Kubernetes liveness and readiness probes.
    Returns 200 if the service is running. A more sophisticated check might
    verify K8s API connectivity, but for a sidecar service, simple is fine.
    """
    return {"status": "healthy", "service": "remediation"}


@app.get("/history")
async def get_history() -> list[dict]:
    """
    Returns recent remediation actions. Useful for debugging and observability.
    The most recent action is last in the list.
    """
    return list(history)


if __name__ == "__main__":
    import uvicorn

    port = int(os.getenv("PORT", "8000"))
    logger.info("starting remediation server on port %d", port)
    uvicorn.run(app, host="0.0.0.0", port=port)
