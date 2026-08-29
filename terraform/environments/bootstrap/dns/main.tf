terraform {
  required_version = ">= 1.6"
  required_providers {
    technitium = {
      source  = "bartei/technitium"
      version = "1.0.1"
    }
  }
}

provider "technitium" {
  server_url = var.technitium_url
  api_token  = var.technitium_api_token
}

module "dns" {
  source  = "../../../modules/dns-records"
  zone    = var.internal_domain
  records = var.records
}

variable "internal_domain" { type = string }
variable "technitium_url" { type = string }
variable "technitium_api_token" {
  type      = string
  sensitive = true
}
variable "records" {
  type = list(object({
    name  = string
    type  = string
    value = string
    ttl   = optional(number, 300)
  }))
}

output "record_names" { value = module.dns.record_names }
