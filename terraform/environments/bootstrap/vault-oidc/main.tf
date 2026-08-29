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

module "oidc" {
  source             = "../../../modules/vault-oidc"
  internal_domain    = var.internal_domain
  oidc_client_id     = var.oidc_client_id
  oidc_client_secret = var.oidc_client_secret
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
variable "oidc_client_id" { type = string }
variable "oidc_client_secret" {
  type      = string
  sensitive = true
}

output "auth_path" { value = module.oidc.auth_path }
