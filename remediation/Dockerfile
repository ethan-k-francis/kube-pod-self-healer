# =============================================================================
# Kube Pod Self-Healer — Remediation Service Dockerfile
#
# Multi-stage isn't needed here (no compilation step), but we use a slim base
# image and follow container best practices:
#   - Non-root user for security
#   - Minimal image layers
#   - Health check built-in
#   - .dockerignore-friendly structure
# =============================================================================

FROM python:3.12-slim

# Metadata labels following OCI image spec conventions
LABEL org.opencontainers.image.title="kube-pod-self-healer-remediation"
LABEL org.opencontainers.image.description="Automated pod remediation service"
LABEL org.opencontainers.image.source="https://github.com/ethan-k-francis/kube-pod-self-healer"

# Set working directory inside the container
WORKDIR /app

# Install dependencies first (separate layer for Docker cache efficiency).
# If requirements.txt doesn't change, Docker reuses this layer even when
# source code changes — dramatically speeding up rebuilds.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code after dependencies (cache-friendly ordering)
COPY . .

# Run as non-root for security. The remediation service doesn't need
# root privileges — it talks to the K8s API over HTTP using a service
# account token, not local system calls.
RUN useradd --create-home --shell /bin/bash appuser
USER appuser

# Expose the FastAPI server port
EXPOSE 8000

# Health check — Kubernetes probes handle this too, but having a Docker-level
# health check helps with local development and docker-compose setups
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')" || exit 1

# Start the FastAPI server with uvicorn
# --host 0.0.0.0 binds to all interfaces (required in containers)
# --workers 1 is fine for a sidecar — one worker handles remediation events sequentially
CMD ["uvicorn", "remediation_server:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
