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
  description = "Nodes to include in the cluster. Provisioning is handled externally."
}

variable "ssh_user" {
  type        = string
  description = "SSH username for connecting to cluster nodes"
}

variable "ssh_key_path" {
  type        = string
  description = "Absolute path to the SSH private key for connecting to cluster nodes"
}

variable "k3s_version" {
  type        = string
  default     = "v1.29.0+k3s1"
  description = "k3s version to install on all cluster nodes"
}

variable "internal_domain" {
  type        = string
  description = "Internal domain (e.g. home.example.com); used in TLS SANs"
}
