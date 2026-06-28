# Kube Pod Self-Healer

**A self-healing Kubernetes demo — detect broken pods and fix common problems automatically**

When apps run in **Kubernetes (K8s)**, containers live in **pods**. Pods crash, run out of memory, fail health checks, or can't pull their container image. Many of these failures have known fixes (restart the pod, scale up, clear a cache). This project watches for those patterns and applies fixes without waiting for a human.

Think of it as a tireless operator that handles the boring, repetitive incidents so you can focus on the ones that need judgment.

---

## What you'll learn

| Term | Plain English |
|---|---|
| **Kubernetes (K8s)** | System that runs and restarts containers across machines |
| **Pod** | One or more containers that run together as a unit |
| **CrashLoopBackOff** | Pod keeps crashing; Kubernetes (K8s) backs off between restart attempts |
| **Out Of Memory Killed (OOMKilled)** | Pod killed because it used too much memory |
| **Health probe** | Periodic "are you alive?" check Kubernetes (K8s) runs against your app |
| **ImagePullBackOff** | Can't download the container image (wrong name, registry down) |
| **Remediation** | Automated fix action (restart, scale, etc.) |
| **GitOps** | Practice of using Git as the source of truth for what should run in the cluster |
| **Argo CD** | Popular GitOps tool that syncs Kubernetes (K8s) state to manifests in Git |
| **Informer** | Efficient watcher that listens for pod status changes via the Kubernetes (K8s) API |

---

## The problem in plain English

On-call engineers get paged for the same failures repeatedly: a pod crash-loops, memory spikes, a bad deploy. The fix is often "delete the pod and let Kubernetes (K8s) recreate it" — work a script can do in seconds. This project automates those known fixes and notifies you via webhook (Slack, Discord, etc.) so you still have visibility.

**Goal:** Turn a ~5 minute manual response into ~30 seconds of automated recovery for well-understood failures — reducing **Mean Time To Recovery (MTTR)**.

---

## How it works

```mermaid
flowchart LR
    subgraph Kubernetes Cluster
        A[K8s API Server] -->|watch pods| B[Go Health Agent]
        B -->|detect failure| C{Failure Type}
        C -->|CrashLoopBackOff| D[Python Remediation Service]
        C -->|OOMKilled| D
        C -->|Failed Probes| D
        C -->|ImagePullBackOff| D
        D -->|restart pod| A
        D -->|scale replicas| A
        D -->|clear cache + restart| A
    end

    B -->|notify| E[Webhook - Slack/Discord/Custom]
    D -->|report result| E
```

**Flow:**

1. **Go Health Agent** watches all pod status via the Kubernetes (K8s) API.
2. When it sees a known failure type, it classifies the event.
3. It sends the event to the **Python Remediation Service** over Hypertext Transfer Protocol (HTTP).
4. The remediation service picks a handler: restart pod, scale deployment, etc.
5. Both detection and fix results go to your **webhook** URL.

Operational details: [docs/runbook.md](docs/runbook.md).

---

## How is this different from Argo CD?

**Argo CD** is a **GitOps** tool — it keeps the cluster aligned with what you committed to Git. It answers: *"Does what's running match the manifests in the repo?"*

**Kube Pod Self-Healer** is a **runtime self-healing** tool — it answers: *"Is what's running actually healthy, and can we auto-fix known failure patterns?"*

They solve different problems and work well together:

| | **Argo CD** | **Kube Pod Self-Healer** |
|---|---|---|
| **Watches** | Cluster state vs. Git repo | Pod health status (crash loops, memory kills, failed probes) |
| **Typical problem** | Config drift, wrong version deployed, manual edits | App crash-looping, out of memory, unhealthy despite correct manifest |
| **Typical fix** | Sync or roll back to the Git-defined desired state | Restart pod, scale replicas, run a remediation handler |
| **Analogy** | Ensures the recipe in the cookbook matches what's on the stove | Notices the dish is burning and turns down the heat |

**Example:** Your Deployment manifest in Git says `replicas: 3` and `image: myapp:v2`. Argo CD is satisfied — the cluster matches Git. All three pods are in **CrashLoopBackOff** because `v2` has a bug. Argo CD will not fix that; the manifest is correct. Kube Pod Self-Healer detects the crash loop and triggers remediation (restart, scale, alert you).

**In a real stack:**

```
Git repo  →  Argo CD deploys and syncs desired state
                ↓
           Pods run in the cluster
                ↓
     Kube Pod Self-Healer watches runtime health and auto-fixes known failures
```

Argo CD handles **deployment and drift from Git**. Kube Pod Self-Healer handles **operational incidents after deploy**. Neither replaces the other.

---

## Quick start

### On your laptop (already cloned)

```bash
# 1. Create a local Kubernetes (K8s) cluster — Kind (Kubernetes IN Docker) runs K8s inside Docker
make cluster-up

# 2. Build images and deploy agent + remediation service
make deploy

# 3. Deploy a deliberately broken pod and watch auto-fix
make demo

# 4. Tear down
make cluster-down
```

### On a remote Linux host (e.g. homelab VM)

Most lab VMs do **not** have a GitHub SSH key. Use **HTTPS**, not `git@github.com`:

```bash
ssh your-host
bash -c "$(curl -fsSL https://raw.githubusercontent.com/ethan-k-francis/kube-pod-self-healer/main/scripts/remote-setup.sh)"
```

Or clone manually, then deploy:

```bash
git clone https://github.com/ethan-k-francis/kube-pod-self-healer.git
cd kube-pod-self-healer
make cluster-up && make deploy
make status
```

If `git clone git@github.com:...` fails with `Permission denied (publickey)`, that is expected — switch to the HTTPS URL above.

---

## What's inside

| Piece | Technology | What it does |
|---|---|---|
| Health agent | Go, client-go | Watches pods, detects failures |
| Remediation | Python, FastAPI | Runs fix actions via the Kubernetes (K8s) API |
| Local cluster | Kind + Terraform | Creates a disposable Kubernetes (K8s) cluster for learning |
| Manifests | Kubernetes YAML | Deploys services with least-privilege Role-Based Access Control (RBAC) |
| Continuous Integration (CI) | GitHub Actions | Lint, test, build on every push |

---

## Project layout

```
kube-pod-self-healer/
├── agent/                    # Go health monitoring agent
├── remediation/              # Python remediation service + handlers
├── terraform/                # Kind cluster provisioning
├── deploy/manifests/         # Kubernetes YAML
├── docs/runbook.md           # Deploy, extend, troubleshoot
└── Makefile
```

---

## Design choices (for the curious)

| Decision | Why |
|---|---|
| Go for the agent | Handles watching hundreds of pods concurrently with low memory |
| Python for fixes | Easy to add new remediation handlers without recompiling |
| Kind for local dev | Real Kubernetes (K8s) API, runs entirely on your laptop |
| FastAPI for remediation API | Simple Hypertext Transfer Protocol (HTTP) service with auto-generated Application Programming Interface (API) docs |

---

## Ideas for extending this

- Prometheus metrics for fix counts and Mean Time To Recovery (MTTR)
- Policy objects — Custom Resource Definitions (CRDs) — to configure which fixes apply where
- Multi-cluster support
- Built-in chaos tests to validate handlers

---

## License

[MIT](LICENSE) — Copyright 2026 Ethan Francis
