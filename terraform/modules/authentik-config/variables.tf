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
