variable "cluster_name" {
  type        = string
  description = "Stable identifier for this k3s cluster (used in Vault paths and Terraform targets)"
}

variable "nodes" {
  type = list(object({
    name    = string
    address = string
    roles   = list(string) # valid values: "control-plane", "worker"
  }))
  description = "Nodes in the Ansible-installed cluster."
}

variable "endpoint" {
  type        = string
  description = "Verified Kubernetes API endpoint."
}

variable "kubeconfig_vault_path" {
  type        = string
  description = "Vault KV path containing the protected kubeconfig written by the Stage 8 workflow."
}
