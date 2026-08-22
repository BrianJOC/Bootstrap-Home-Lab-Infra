terraform {
  required_version = ">= 1.6"
}

locals {
  control_planes = [for node in var.nodes : node if contains(node.roles, "control-plane")]
  workers        = [for node in var.nodes : node if contains(node.roles, "worker") && !contains(node.roles, "control-plane")]
}

# This module deliberately does not connect to a host or install k3s. Ansible
# owns that lifecycle. Terraform validates and records the durable cluster
# registration consumed by downstream modules and GitOps.
resource "terraform_data" "cluster_registration" {
  input = {
    name                  = var.cluster_name
    endpoint              = var.endpoint
    kubeconfig_vault_path = var.kubeconfig_vault_path
    nodes                 = var.nodes
  }

  lifecycle {
    precondition {
      condition     = length(local.control_planes) > 0
      error_message = "A k3s cluster requires at least one control-plane node."
    }

    precondition {
      condition = alltrue(flatten([
        for node in var.nodes : [
          for role in node.roles : contains(["control-plane", "worker"], role)
        ]
      ]))
      error_message = "Node roles must be control-plane or worker."
    }
  }
}
