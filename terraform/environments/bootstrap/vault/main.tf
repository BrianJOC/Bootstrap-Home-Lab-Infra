terraform {
  required_version = ">= 1.6"
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "3.25.0"
    }
  }
}

provider "vault" {
  address         = var.vault_address
  token           = var.vault_token
  skip_tls_verify = var.skip_tls_verify
}

module "vault" { source = "../../../modules/vault-config" }

variable "vault_address" { type = string }
variable "vault_token" {
  type      = string
  sensitive = true
}
variable "skip_tls_verify" {
  type    = bool
  default = false
}

output "kv_mount_path" { value = module.vault.kv_mount_path }
