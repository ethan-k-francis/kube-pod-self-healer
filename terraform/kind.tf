# =============================================================================
# Kube Remediator — Kind Cluster Resource
#
# Defines the Kind cluster with a control plane and configurable worker nodes.
# Extra port mappings expose NodePort services to the host machine, which is
# essential for accessing services running inside the Kind cluster from your
# local development environment.
# =============================================================================

resource "kind_cluster" "kube_remediator" {
  name           = var.cluster_name
  node_image     = "kindest/node:${var.k8s_version}"
  wait_for_ready = true

  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"

    # --- Control Plane Node ---
    # The control plane runs the API server, etcd, scheduler, and controller
    # manager. Extra port mappings let us reach NodePort services from the host.
    node {
      role = "control-plane"

      # Map host port 30080 -> container port 30080 for NodePort services.
      # This is how you access services in the Kind cluster from localhost.
      extra_port_mappings {
        container_port = 30080
        host_port      = 30080
        protocol       = "TCP"
      }

      # Map host port 30443 for HTTPS NodePort services
      extra_port_mappings {
        container_port = 30443
        host_port      = 30443
        protocol       = "TCP"
      }
    }

    # --- Worker Nodes ---
    # Workers run the actual workload pods. Using dynamic blocks to create
    # a configurable number of workers from the node_count variable.
    dynamic "node" {
      for_each = range(var.node_count)
      content {
        role = "worker"
      }
    }
  }
}
