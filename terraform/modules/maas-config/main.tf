terraform {
  required_version = ">= 1.6"
  required_providers {
    maas = {
      source  = "canonical/maas"
      version = "2.8.0"
    }
  }
}

# ── Network Topology ──────────────────────────────────────────────────────────

resource "maas_configuration" "upstream_dns" {
  key   = "upstream_dns"
  value = var.dns_server
}

resource "maas_configuration" "site_name" {
  key   = "maas_name"
  value = "maas.${var.internal_domain}"
}

resource "maas_space" "homelab" {
  name = "homelab"
}

resource "maas_fabric" "homelab" {
  name = "homelab-fabric"
}

resource "maas_vlan" "lab" {
  fabric = maas_fabric.homelab.id
  vid    = var.lab_vlan_id
  name   = "lab-vlan"
  space  = maas_space.homelab.name
  mtu    = 1500
}

resource "maas_subnet" "lab" {
  cidr        = var.lab_subnet
  fabric      = maas_fabric.homelab.id
  vlan        = maas_vlan.lab.id
  name        = "lab-subnet"
  gateway_ip  = var.lab_gateway
  dns_servers = [var.dns_server]

  dynamic "ip_ranges" {
    for_each = var.maas_dhcp_enabled ? [1] : []
    content {
      type     = "dynamic"
      start_ip = var.dhcp_range_low
      end_ip   = var.dhcp_range_high
      comment  = "MAAS-managed DHCP dynamic range for the explicitly approved lab VLAN"
    }
  }
}

# ── DHCP ──────────────────────────────────────────────────────────────────────
# MAAS owns DHCP on the lab VLAN; Unifi must be configured to NOT serve DHCP
# on this segment and to trunk the VLAN to the MAAS server's network interface.

resource "maas_subnet_ip_range" "reserved" {
  subnet   = maas_subnet.lab.id
  type     = "reserved"
  start_ip = var.lab_gateway
  end_ip   = var.lab_gateway
  comment  = "Reserved: lab gateway IP"
}
