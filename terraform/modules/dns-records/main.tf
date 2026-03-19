terraform {
  required_version = ">= 1.6"
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "~> 3.0"
    }
  }
}

# Technitium has no official Terraform provider. We use null_resource with
# local-exec curl calls against the Technitium HTTP API. Each record is its
# own resource keyed on record content so any value change triggers a replace.

locals {
  records_map = { for r in var.records : "${r.name}-${r.type}" => r }
}

resource "null_resource" "dns_record" {
  for_each = local.records_map

  triggers = {
    name  = each.value.name
    type  = each.value.type
    value = each.value.value
    ttl   = tostring(each.value.ttl)
    zone  = var.zone
    url   = var.technitium_url
  }

  # Create / upsert record
  provisioner "local-exec" {
    command = <<-EOT
      set -e
      FQDN="${each.value.name}.${var.zone}"
      # Delete existing record with this name/type first (idempotent upsert)
      curl -sf -o /dev/null \
        "${var.technitium_url}/api/zones/records/delete" \
        -X POST \
        -H "Authorization: Token ${var.technitium_api_token}" \
        -d "token=${var.technitium_api_token}" \
        -d "domain=${var.zone}" \
        -d "name=${each.value.name}" \
        -d "type=${each.value.type}" \
        || true   # ignore if record did not exist

      # Add the record
      curl -sf \
        "${var.technitium_url}/api/zones/records/add" \
        -X POST \
        -H "Authorization: Token ${var.technitium_api_token}" \
        -d "token=${var.technitium_api_token}" \
        -d "domain=${var.zone}" \
        -d "name=${each.value.name}" \
        -d "type=${each.value.type}" \
        -d "ttl=${each.value.ttl}" \
        -d "ipAddress=${each.value.value}" \
        -d "overwrite=true"

      echo "Created DNS record: $FQDN ${each.value.type} ${each.value.value}"
    EOT
  }

  # Delete record on destroy
  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      set -e
      curl -sf -o /dev/null \
        "${self.triggers.url}/api/zones/records/delete" \
        -X POST \
        -d "token=${var.technitium_api_token}" \
        -d "domain=${self.triggers.zone}" \
        -d "name=${self.triggers.name}" \
        -d "type=${self.triggers.type}" \
        || true
      echo "Deleted DNS record: ${self.triggers.name}.${self.triggers.zone} ${self.triggers.type}"
    EOT
  }
}
