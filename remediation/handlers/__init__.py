"""
Remediation Handlers Package

Each handler implements a specific remediation strategy. Handlers follow a
simple interface: they accept parameters describing the target pod/deployment
and return a tuple of (success: bool, message: str).

This makes handlers easy to test independently and extend — adding a new
remediation strategy is just adding a new module to this package.

Available handlers:
    restart_pod    — Delete the pod to trigger a fresh restart
    clear_cache    — Exec into the pod to clear cached state, fall back to restart
    scale_replicas — Scale up the parent Deployment to add capacity
"""

from handlers.restart_pod import restart_pod
from handlers.clear_cache import clear_cache
from handlers.scale_replicas import scale_replicas

__all__ = ["restart_pod", "clear_cache", "scale_replicas"]
