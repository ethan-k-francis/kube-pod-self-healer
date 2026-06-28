# =============================================================================
# Kube Remediator — Health Agent Dockerfile
#
# Multi-stage build for the Go health monitoring agent:
#   Stage 1 (builder): compiles the Go binary with all k8s client-go deps
#   Stage 2 (runtime): copies the static binary into a distroless image
#
# Distroless has no shell, no package manager, no attack surface — just the
# binary and CA certificates. This is the gold standard for Go containers.
# =============================================================================

# ── Stage 1: Build ────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy dependency manifests first for Docker layer caching.
# go mod download only re-runs when go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and compile a statically-linked binary.
# CGO_ENABLED=0 ensures no C library deps (pure Go).
# -ldflags="-s -w" strips debug info, reducing binary size ~30%.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o health-agent ./cmd/agent

# ── Stage 2: Runtime ──────────────────────────────────────
# Distroless nonroot runs as uid 65532 by default — no USER directive needed.
# Includes CA certificates for TLS connections to the K8s API server.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /build/health-agent .

ENTRYPOINT ["./health-agent"]
