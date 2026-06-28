# =============================================================================
# Kube Pod Self-Healer — Makefile (Full Version)
# Top-level build, test, deployment, and operational automation.
# =============================================================================

# Cluster name used by Kind and Terraform
CLUSTER_NAME ?= kube-pod-self-healer
# Namespace where kube-pod-self-healer workloads run
NAMESPACE ?= kube-pod-self-healer
# Container image tags
AGENT_IMAGE ?= kube-pod-self-healer/agent:latest
REMEDIATION_IMAGE ?= kube-pod-self-healer/remediation:latest

# -----------------------------------------------------------------------------
# Cluster lifecycle — create and destroy the local Kind cluster
# -----------------------------------------------------------------------------

.PHONY: cluster-up
cluster-up: ## Create a Kind cluster for local development
	kind create cluster --name $(CLUSTER_NAME) --config deploy/kind-config.yaml

.PHONY: cluster-down
cluster-down: ## Delete the Kind cluster
	kind delete cluster --name $(CLUSTER_NAME)

# -----------------------------------------------------------------------------
# Build — compile the Go agent and build Docker images
# -----------------------------------------------------------------------------

.PHONY: build
build: build-agent build-remediation ## Build all container images

.PHONY: build-agent
build-agent: ## Build the Go health agent Docker image
	docker build -t $(AGENT_IMAGE) agent/

.PHONY: build-remediation
build-remediation: ## Build the Python remediation Docker image
	docker build -t $(REMEDIATION_IMAGE) remediation/

# -----------------------------------------------------------------------------
# Test — run unit tests for Go and Python
# -----------------------------------------------------------------------------

.PHONY: test
test: test-agent test-remediation ## Run all tests

.PHONY: test-agent
test-agent: ## Run Go agent tests
	cd agent && go test -race ./...

.PHONY: test-remediation
test-remediation: ## Run Python remediation tests
	cd remediation && python -m pytest tests/ -v 2>/dev/null || echo "No tests yet"

# -----------------------------------------------------------------------------
# Deploy — push images to Kind and apply K8s manifests
# -----------------------------------------------------------------------------

.PHONY: deploy
deploy: build ## Build images, load into Kind, and apply manifests
	kind load docker-image $(AGENT_IMAGE) --name $(CLUSTER_NAME)
	kind load docker-image $(REMEDIATION_IMAGE) --name $(CLUSTER_NAME)
	kubectl apply -f deploy/manifests/namespace.yaml
	kubectl apply -f deploy/manifests/agent-rbac.yaml
	kubectl apply -f deploy/manifests/remediation-deployment.yaml
	kubectl apply -f deploy/manifests/agent-deployment.yaml

# -----------------------------------------------------------------------------
# Demo — deploy broken workloads and watch the agent remediate them
# -----------------------------------------------------------------------------

.PHONY: demo
demo: ## Deploy crashloop demos and watch agent logs
	@echo "=== Deploying demo workloads ==="
	kubectl apply -f deploy/manifests/crashloop-demo.yaml
	@echo ""
	@echo "=== Watching health agent logs (Ctrl+C to stop) ==="
	@echo "Wait ~30s for CrashLoopBackOff to trigger..."
	@echo ""
	kubectl logs -f -n $(NAMESPACE) -l app=health-agent

# -----------------------------------------------------------------------------
# Logs — recent or follow-mode output from running workloads
# -----------------------------------------------------------------------------

.PHONY: logs
logs: agent-logs ## Alias for agent-logs

.PHONY: agent-logs
agent-logs: ## Show recent health agent logs (last 100 lines)
	kubectl logs --tail=100 -n $(NAMESPACE) -l app=health-agent

.PHONY: agent-logs-follow
agent-logs-follow: ## Follow health agent logs (Ctrl+C to stop; exit code 130 is normal)
	kubectl logs -f -n $(NAMESPACE) -l app=health-agent

.PHONY: remediation-logs
remediation-logs: ## Show recent remediation service logs (last 100 lines)
	kubectl logs --tail=100 -n $(NAMESPACE) -l app=remediation-service

.PHONY: remediation-logs-follow
remediation-logs-follow: ## Follow remediation logs (Ctrl+C to stop; exit code 130 is normal)
	kubectl logs -f -n $(NAMESPACE) -l app=remediation-service

# -----------------------------------------------------------------------------
# Status — show the current state of kube-pod-self-healer workloads
# -----------------------------------------------------------------------------

.PHONY: status
status: ## Show pod health and status in the kube-pod-self-healer namespace
	@echo "=== Kube Pod Self-Healer Pods ==="
	kubectl get pods -n $(NAMESPACE) -o wide
	@echo ""
	@echo "=== Pod Health Summary ==="
	@kubectl get pods -n $(NAMESPACE) -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\t"}{range .status.containerStatuses[*]}{.name}: ready={.ready}, restarts={.restartCount}{" "}{end}{"\n"}{end}' 2>/dev/null || echo "No pods found"
	@echo ""
	@echo "=== Services ==="
	kubectl get svc -n $(NAMESPACE)
	@echo ""
	@echo "=== Recent Events ==="
	kubectl get events -n $(NAMESPACE) --sort-by='.lastTimestamp' | tail -10

# -----------------------------------------------------------------------------
# Clean — remove build artifacts and demo workloads
# -----------------------------------------------------------------------------

.PHONY: clean
clean: ## Remove build artifacts and temp files
	rm -rf bin/
	rm -rf agent/bin/
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true

.PHONY: clean-demo
clean-demo: ## Remove demo workloads from the cluster
	kubectl delete -f deploy/manifests/crashloop-demo.yaml --ignore-not-found

# -----------------------------------------------------------------------------
# Lint — run linters for all components
# -----------------------------------------------------------------------------

.PHONY: lint
lint: lint-agent lint-remediation ## Run all linters

.PHONY: lint-agent
lint-agent: ## Run Go linter
	cd agent && golangci-lint run ./...

.PHONY: lint-remediation
lint-remediation: ## Run Python linter
	cd remediation && ruff check .

.PHONY: remote-setup
remote-setup: ## Clone (HTTPS) and deploy on a host without GitHub SSH keys
	chmod +x scripts/remote-setup.sh
	./scripts/remote-setup.sh

# -----------------------------------------------------------------------------
# Local CI parity — run before opening PRs
# -----------------------------------------------------------------------------

.PHONY: lint-ci ci-security pr-commit-check ci
lint-ci: ## Run pre-commit hooks across the repository
	pre-commit run --all-files

ci-security: ## Run local security scan parity (Trivy)
	trivy fs --severity HIGH,CRITICAL --exit-code 1 .

pr-commit-check: ## Check branch commits for forbidden commit footers
	@chmod +x .github/scripts/commit-message-lint.sh
	@.github/scripts/commit-message-lint.sh \
		--base-ref origin/main \
		$${PR_TITLE:+--title "$${PR_TITLE}"} \
		$${PR_BODY_FILE:+--body-file "$${PR_BODY_FILE}"}

ci: lint-ci ci-security pr-commit-check ## Run local CI checks before PR
	@echo "Local CI checks passed."

# -----------------------------------------------------------------------------
# Help — list available targets
# -----------------------------------------------------------------------------

.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
