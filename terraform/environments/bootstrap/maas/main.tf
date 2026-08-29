terraform {
  required_version = ">= 1.6"
  required_providers {
    maas = {
      source  = "canonical/maas"
      version = "2.8.0"
    }
  }
}

provider "maas" {
  api_version = "2.0"
  api_key     = var.maas_api_key
  api_url     = var.maas_api_url
}

module "maas" {
  source            = "../../../modules/maas-config"
  internal_domain   = var.internal_domain
  lab_subnet        = var.lab_subnet
  lab_vlan_id       = var.lab_vlan_id
  dns_server        = var.dns_server
  lab_gateway       = var.lab_gateway
  dhcp_range_low    = var.dhcp_range_low
  dhcp_range_high   = var.dhcp_range_high
  maas_dhcp_enabled = var.maas_dhcp_enabled
}

variable "maas_api_url" { type = string }
variable "maas_api_key" {
  type      = string
  sensitive = true
}
variable "internal_domain" { type = string }
variable "lab_subnet" { type = string }
variable "lab_vlan_id" { type = number }
variable "dns_server" { type = string }
variable "lab_gateway" { type = string }
variable "dhcp_range_low" { type = string }
variable "dhcp_range_high" { type = string }
variable "maas_dhcp_enabled" { type = bool }

output "space_name" { value = module.maas.space_name }
