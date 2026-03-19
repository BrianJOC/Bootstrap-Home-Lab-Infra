variable "maas_api_url" {
  type        = string
  description = "MAAS API endpoint URL (e.g. http://maas.home.example.com:5240/MAAS)"
}

variable "maas_api_key" {
  type        = string
  sensitive   = true
  description = "MAAS API key (consumer_key:token_key:token_secret format)"
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
