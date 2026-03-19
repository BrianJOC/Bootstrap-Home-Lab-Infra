variable "vault_address" {
  type        = string
  description = "Vault server address (e.g. https://vault.home.example.com:8200)"
}

variable "vault_token" {
  type        = string
  sensitive   = true
  description = "Vault root or bootstrap token used for configuration"
}

variable "internal_domain" {
  type        = string
  description = "Internal domain (e.g. home.example.com)"
}

variable "cert_path" {
  type        = string
  description = "Absolute path to the fullchain.pem on the operator's machine"
}

variable "key_path" {
  type        = string
  description = "Absolute path to the privkey.pem on the operator's machine"
}
