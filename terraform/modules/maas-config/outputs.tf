output "space_name" {
  description = "MAAS space name for the homelab network"
  value       = maas_space.homelab.name
}

output "subnet_cidr" {
  description = "CIDR of the managed lab subnet"
  value       = maas_subnet.lab.cidr
}

output "fabric_id" {
  description = "MAAS fabric ID containing the lab VLAN"
  value       = maas_fabric.homelab.id
}

output "vlan_id" {
  description = "MAAS VLAN resource ID (not the 802.1q VLAN tag)"
  value       = maas_vlan.lab.id
}
