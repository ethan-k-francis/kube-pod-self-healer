# =============================================================================
# Infra Autopilot — Terraform Variables
#
# Input variables for the Kind cluster. These let you customize the cluster
# without editing the resource definitions directly. Override them with
# terraform.tfvars, -var flags, or TF_VAR_ environment variables.
# =============================================================================

variable "cluster_name" {
  description = "Name of the Kind cluster. Also used as the kubectl context name."
  type        = string
  default     = "autopilot"
}

variable "node_count" {
  description = "Number of worker nodes. Control plane is always 1."
  type        = number
  default     = 2

  validation {
    condition     = var.node_count >= 1 && var.node_count <= 10
    error_message = "Worker node count must be between 1 and 10."
  }
}

variable "k8s_version" {
  description = "Kubernetes version for the Kind nodes (maps to a kindest/node image tag)."
  type        = string
  default     = "v1.31.4"
}
