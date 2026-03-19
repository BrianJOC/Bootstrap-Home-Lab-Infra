variable "gitlab_url" {
  type        = string
  description = "GitLab base URL (e.g. https://gitlab.home.example.com)"
}

variable "gitlab_token" {
  type        = string
  sensitive   = true
  description = "GitLab personal access token with api scope"
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
