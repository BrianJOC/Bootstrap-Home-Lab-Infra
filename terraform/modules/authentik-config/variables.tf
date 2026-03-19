variable "authentik_url" {
  type        = string
  description = "Authentik base URL (e.g. https://authentik.home.example.com)"
}

variable "authentik_token" {
  type        = string
  sensitive   = true
  description = "Authentik API token with admin privileges"
}

variable "internal_domain" {
  type        = string
  description = "Internal domain (e.g. home.example.com)"
}

variable "services" {
  type = list(object({
    name         = string
    redirect_uri = string
    groups       = list(string)
  }))
  description = "List of services to create OIDC providers and applications for"
}
