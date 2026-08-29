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

module "gitlab" {
  source          = "../../../modules/vault-gitlab-jwt"
  internal_domain = var.internal_domain
}

variable "vault_address" { type = string }
variable "vault_token" {
  type      = string
  sensitive = true
}
variable "skip_tls_verify" {
  type    = bool
  default = false
}
variable "internal_domain" { type = string }

output "auth_path" { value = module.gitlab.auth_path }
