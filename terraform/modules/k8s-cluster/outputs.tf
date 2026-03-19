output "cluster_name" {
  description = "Name of the k3s cluster"
  value       = var.cluster_name
}

output "kubeconfig_command" {
  description = "SSH command to retrieve the kubeconfig from the first control-plane node"
  value       = "ssh ${var.ssh_user}@${local.control_planes[0].address} 'sudo cat /etc/rancher/k3s/k3s.yaml'"
}

output "control_plane_addresses" {
  description = "IP addresses of all control-plane nodes"
  value       = [for n in local.control_planes : n.address]
}

output "worker_addresses" {
  description = "IP addresses of all worker-only nodes"
  value       = [for n in local.workers : n.address]
}
