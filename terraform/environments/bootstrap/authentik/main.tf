terraform {
  required_version = ">= 1.6"
  required_providers {
    authentik = {
      source  = "goauthentik/authentik"
      version = "2024.2.0"
    }
  }
}

provider "authentik" {
  url   = var.authentik_url
  token = var.authentik_token
}

module "authentik" {
  source          = "../../../modules/authentik-config"
  internal_domain = var.internal_domain
  services        = var.services
}

variable "authentik_url" { type = string }
variable "authentik_token" {
  type      = string
  sensitive = true
}
variable "internal_domain" { type = string }
variable "services" {
  type = list(object({
    name         = string
    redirect_uri = string
    groups       = list(string)
  }))
}

output "client_ids" { value = module.authentik.client_ids }
output "client_secrets" {
  value     = module.authentik.client_secrets
  sensitive = true
}
