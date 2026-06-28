# =============================================================================
# Infra Autopilot — Terraform Main Configuration
#
# This is the root Terraform configuration. It sets the required Terraform
# version and configures local state storage. For a production system you'd
# use remote state (S3, GCS, Terraform Cloud), but local state is appropriate
# for a Kind-based dev cluster that's ephemeral by nature.
# =============================================================================

terraform {
  # Minimum Terraform version — ensures consistent provider behavior
  required_version = ">= 1.9.0"

  # Local backend stores state on disk. Fine for dev clusters that are
  # created and destroyed frequently. Never use local state for shared infra.
  backend "local" {
    path = "terraform.tfstate"
  }
}
