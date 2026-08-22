output "cluster_name" {
  description = "Name of the k3s cluster"
  value       = terraform_data.cluster_registration.output.name
}

output "endpoint" {
  description = "Verified Kubernetes API endpoint"
  value       = terraform_data.cluster_registration.output.endpoint
}

output "kubeconfig_vault_path" {
  description = "Vault path for the cluster kubeconfig"
  value       = terraform_data.cluster_registration.output.kubeconfig_vault_path
}

output "control_plane_addresses" {
  description = "IP addresses of all control-plane nodes"
  value       = [for n in local.control_planes : n.address]
}

output "worker_addresses" {
  description = "IP addresses of all worker-only nodes"
  value       = [for n in local.workers : n.address]
}
