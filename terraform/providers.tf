# =============================================================================
# Infra Autopilot — Provider Configuration
#
# Declares the Kind provider, which lets Terraform manage Kind clusters as
# native resources. The tehcyx/kind provider wraps the Kind CLI into a
# Terraform-compatible lifecycle (create on apply, destroy on destroy).
# =============================================================================

terraform {
  required_providers {
    # The Kind provider creates real Kubernetes clusters inside Docker
    # containers. Each "node" is a Docker container running kubelet.
    kind = {
      source  = "tehcyx/kind"
      version = "~> 0.7.0"
    }
  }
}

# The Kind provider needs no explicit configuration — it uses the local
# Docker daemon automatically. If you needed a remote Docker host, you'd
# set DOCKER_HOST in the environment.
provider "kind" {}
