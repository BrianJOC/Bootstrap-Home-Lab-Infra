variable "gitlab_url" {
  type        = string
  description = "GitLab base URL used in generated backend examples."
}

variable "vault_address" {
  type        = string
  description = "Vault server address — stored as project CI/CD variable"
}

variable "vault_jwt_auth_path" {
  type        = string
  default     = "jwt"
  description = "Path of the JWT auth method in Vault (used by GitLab CI pipelines)"
}

variable "internal_domain" {
  type        = string
  description = "Internal domain (e.g. home.example.com)"
}

variable "maas_api_url" {
  type        = string
  description = "MAAS API URL exposed to the day-2 Terraform pipeline."
}
