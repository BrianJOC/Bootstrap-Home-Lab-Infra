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
