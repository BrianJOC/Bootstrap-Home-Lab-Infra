terraform {
  required_version = ">= 1.6"
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "~> 3.0"
    }
  }
}

locals {
  control_planes = [for n in var.nodes : n if contains(n.roles, "control-plane")]
  workers        = [for n in var.nodes : n if !contains(n.roles, "control-plane")]
}

# ── Control Plane Nodes ───────────────────────────────────────────────────────
# Install k3s server on each control-plane node.
# The first node bootstraps the cluster (--cluster-init); subsequent nodes join.

resource "null_resource" "k3s_control_plane_primary" {
  # First control-plane node: bootstraps the cluster
  count = length(local.control_planes) > 0 ? 1 : 0

  triggers = {
    node_address = local.control_planes[0].address
    k3s_version  = var.k3s_version
    cluster_name = var.cluster_name
  }

  connection {
    type        = "ssh"
    host        = local.control_planes[0].address
    user        = var.ssh_user
    private_key = file(var.ssh_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if systemctl is-active k3s > /dev/null 2>&1; then echo 'k3s already running on primary control-plane; skipping install'; exit 0; fi",
      "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='${var.k3s_version}' sh -s - server --cluster-init --tls-san ${local.control_planes[0].address} --cluster-domain ${var.cluster_name}.${var.internal_domain}",
      "echo 'k3s control-plane (primary) installed on ${local.control_planes[0].address}'",
    ]
  }
}

resource "null_resource" "k3s_control_plane_additional" {
  # Additional control-plane nodes join the existing cluster
  for_each = {
    for i, n in local.control_planes : n.name => n
    if i > 0 # skip the primary (index 0)
  }

  depends_on = [null_resource.k3s_control_plane_primary]

  triggers = {
    node_address   = each.value.address
    primary_address = local.control_planes[0].address
    k3s_version    = var.k3s_version
  }

  connection {
    type        = "ssh"
    host        = each.value.address
    user        = var.ssh_user
    private_key = file(var.ssh_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if systemctl is-active k3s > /dev/null 2>&1; then echo 'k3s already running on ${each.key}; skipping install'; exit 0; fi",
      # Fetch the cluster-join token from the primary control-plane node
      "K3S_TOKEN=$(ssh -o StrictHostKeyChecking=no -i ${var.ssh_key_path} ${var.ssh_user}@${local.control_planes[0].address} 'sudo cat /var/lib/rancher/k3s/server/node-token')",
      "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='${var.k3s_version}' K3S_TOKEN=\"$K3S_TOKEN\" sh -s - server --server https://${local.control_planes[0].address}:6443 --tls-san ${each.value.address}",
    ]
  }
}

# ── Worker Nodes ──────────────────────────────────────────────────────────────

resource "null_resource" "k3s_worker" {
  for_each = { for n in local.workers : n.name => n }

  depends_on = [null_resource.k3s_control_plane_primary]

  triggers = {
    node_address   = each.value.address
    primary_address = local.control_planes[0].address
    k3s_version    = var.k3s_version
  }

  connection {
    type        = "ssh"
    host        = each.value.address
    user        = var.ssh_user
    private_key = file(var.ssh_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if systemctl is-active k3s-agent > /dev/null 2>&1; then echo 'k3s-agent already running on ${each.key}; skipping install'; exit 0; fi",
      "K3S_TOKEN=$(ssh -o StrictHostKeyChecking=no -i ${var.ssh_key_path} ${var.ssh_user}@${local.control_planes[0].address} 'sudo cat /var/lib/rancher/k3s/server/node-token')",
      "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='${var.k3s_version}' K3S_URL=https://${local.control_planes[0].address}:6443 K3S_TOKEN=\"$K3S_TOKEN\" sh -",
      "echo 'k3s worker agent installed on ${each.key}'",
    ]
  }
}
