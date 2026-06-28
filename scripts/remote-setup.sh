#!/usr/bin/env bash
# Bootstrap kube-pod-self-healer on a Linux host without GitHub SSH keys
# (e.g. homelab VM). Clones via HTTPS, creates Kind cluster, deploys stack.
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/ethan-k-francis/kube-pod-self-healer.git}"
INSTALL_DIR="${1:-${HOME}/kube-pod-self-healer}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: '$1' not found — install it before running this script" >&2
    exit 1
  fi
}

for cmd in git docker kind kubectl make; do
  require_cmd "$cmd"
done

if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon not reachable — add your user to the docker group or run with sudo" >&2
  exit 1
fi

# HTTPS works on public hosts; SSH (git@github.com) fails without a deploy key.
if [[ ! -d "${INSTALL_DIR}/.git" ]]; then
  echo "=== Cloning ${REPO_URL} -> ${INSTALL_DIR} ==="
  git clone "${REPO_URL}" "${INSTALL_DIR}"
else
  echo "=== Repo already present at ${INSTALL_DIR} — pulling latest ==="
  git -C "${INSTALL_DIR}" pull --ff-only origin main
fi

cd "${INSTALL_DIR}"

echo "=== Tearing down old Kind cluster (ok if none exists) ==="
make cluster-down || true

echo "=== Creating cluster and deploying ==="
make cluster-up
make deploy

echo ""
echo "=== Ready ==="
echo "  cd ${INSTALL_DIR}"
echo "  make status              # pod overview"
echo "  make demo                # deploy broken pods + stream agent logs"
echo "  make agent-logs          # last 100 lines from health agent"
echo "  make remediation-logs    # last 100 lines from remediation service"
