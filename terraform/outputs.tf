# =============================================================================
# Kube Pod Self-Healer — Terraform Outputs
#
# Outputs expose values from the Terraform state so they can be consumed by
# scripts, CI pipelines, or other Terraform modules. After `terraform apply`,
# run `terraform output` to see these values.
# =============================================================================

output "cluster_name" {
  description = "The name of the Kind cluster"
  value       = kind_cluster.kube_remediator.name
}

output "kubeconfig_path" {
  description = "Path to the generated kubeconfig file for the cluster"
  value       = kind_cluster.kube_remediator.kubeconfig_path
}

output "endpoint" {
  description = "The API server endpoint of the Kind cluster"
  value       = kind_cluster.kube_remediator.endpoint
}
