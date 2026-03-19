variable "technitium_url" {
  type        = string
  description = "Base URL of the Technitium DNS API (e.g. http://192.168.1.10:5380)"
}

variable "technitium_api_token" {
  type        = string
  sensitive   = true
  description = "Technitium API token for authentication"
}

variable "zone" {
  type        = string
  description = "DNS zone name (e.g. home.example.com)"
}

variable "records" {
  type = list(object({
    name  = string
    type  = string
    value = string
    ttl   = optional(number, 300)
  }))
  description = "List of DNS records to create in the zone"
}
