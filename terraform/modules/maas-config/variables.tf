variable "internal_domain" {
  type        = string
  description = "Internal domain used for the MAAS site name."
}

variable "lab_subnet" {
  type        = string
  description = "CIDR of the lab VLAN subnet (e.g. 10.10.10.0/24)"
}

variable "lab_vlan_id" {
  type        = number
  description = "VLAN ID for the lab segment as configured on Unifi"
}

variable "dns_server" {
  type        = string
  description = "IP address of the Technitium DNS server to configure as MAAS upstream"
}

variable "lab_gateway" {
  type        = string
  description = "Default gateway IP for the lab subnet"
}

variable "dhcp_range_low" {
  type        = string
  description = "Start IP of the DHCP dynamic range within the lab subnet"
}

variable "dhcp_range_high" {
  type        = string
  description = "End IP of the DHCP dynamic range within the lab subnet"
}

variable "maas_dhcp_enabled" {
  type        = bool
  default     = false
  description = "Opt in to a MAAS dynamic range on the single validated lab VLAN."

  validation {
    condition = !var.maas_dhcp_enabled || (
      var.dhcp_range_low != "" && var.dhcp_range_high != ""
    )
    error_message = "Enabling MAAS DHCP requires both range endpoints."
  }
}
