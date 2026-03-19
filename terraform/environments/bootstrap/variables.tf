# ── Common ────────────────────────────────────────────────────────────────────

variable "internal_domain" {
  type        = string
  description = "Internal domain (e.g. home.example.com)"
}

variable "bootstrap_server_ip" {
  type        = string
  description = "Static IP of the bootstrap server (used for all service A records)"
}

# ── DNS (Technitium) ──────────────────────────────────────────────────────────

variable "technitium_url" {
  type        = string
  description = "Base URL of the Technitium DNS API (e.g. http://192.168.1.10:5380)"
}

variable "technitium_api_token" {
  type        = string
  sensitive   = true
  description = "Technitium API token"
}

variable "dns_records" {
  type = list(object({
    name  = string
    type  = string
    value = string
    ttl   = optional(number, 300)
  }))
  description = "DNS records to create for all bootstrap services"
  default = []
}

# ── Vault ─────────────────────────────────────────────────────────────────────

variable "vault_address" {
  type        = string
  description = "Vault server HTTPS address (e.g. https://vault.home.example.com:8200)"
}

variable "vault_token" {
  type        = string
  sensitive   = true
  description = "Vault bootstrap token (24h TTL; sourced from run context)"
}

variable "cert_path" {
  type        = string
  description = "Absolute path to fullchain.pem on the operator's machine"
}

variable "key_path" {
  type        = string
  description = "Absolute path to privkey.pem on the operator's machine"
}

# ── Authentik ─────────────────────────────────────────────────────────────────

variable "authentik_url" {
  type        = string
  description = "Authentik base URL (e.g. https://authentik.home.example.com)"
}

variable "authentik_token" {
  type        = string
  sensitive   = true
  description = "Authentik API token"
}

variable "authentik_services" {
  type = list(object({
    name         = string
    redirect_uri = string
    groups       = list(string)
  }))
  description = "OIDC services to register in Authentik"
  default = []
}

# ── GitLab ────────────────────────────────────────────────────────────────────

variable "gitlab_url" {
  type        = string
  description = "GitLab base URL (e.g. https://gitlab.home.example.com)"
}

variable "gitlab_token" {
  type        = string
  sensitive   = true
  description = "GitLab personal access token with api scope"
}

# ── MAAS ──────────────────────────────────────────────────────────────────────

variable "maas_api_url" {
  type        = string
  description = "MAAS API endpoint URL"
}

variable "maas_api_key" {
  type        = string
  sensitive   = true
  description = "MAAS API key (consumer_key:token_key:token_secret)"
}

variable "lab_subnet" {
  type        = string
  description = "CIDR of the lab VLAN subnet (e.g. 10.10.10.0/24)"
}

variable "lab_vlan_id" {
  type        = number
  description = "VLAN ID for the lab segment"
}

variable "lab_gateway" {
  type        = string
  description = "Default gateway IP for the lab subnet"
}

variable "dhcp_range_low" {
  type        = string
  description = "Start IP of the MAAS DHCP dynamic range"
}

variable "dhcp_range_high" {
  type        = string
  description = "End IP of the MAAS DHCP dynamic range"
}
