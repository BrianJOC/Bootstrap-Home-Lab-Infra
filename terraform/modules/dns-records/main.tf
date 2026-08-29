terraform {
  required_version = ">= 1.6"
  required_providers {
    technitium = {
      source  = "bartei/technitium"
      version = "1.0.1"
    }
  }
}

locals {
  records_map = { for r in var.records : "${r.name}-${r.type}" => r }
}

resource "technitium_zone" "internal" {
  name = var.zone
  type = "Primary"
}

resource "technitium_record" "dns_record" {
  for_each = local.records_map

  zone      = technitium_zone.internal.name
  name      = "${each.value.name}.${var.zone}"
  type      = each.value.type
  value     = each.value.value
  ttl       = each.value.ttl
  overwrite = true
}
