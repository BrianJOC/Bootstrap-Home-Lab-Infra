terraform {
  required_version = ">= 1.6"
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "3.25.0"
    }
  }
}

resource "vault_mount" "kv" {
  path        = "secret"
  type        = "kv"
  options     = { version = "2" }
  description = "KV v2 secrets engine for all bootstrap and service secrets"
}

resource "vault_mount" "pki" {
  path                      = "pki"
  type                      = "pki"
  description               = "PKI secrets engine"
  default_lease_ttl_seconds = 86400
  max_lease_ttl_seconds     = 365 * 24 * 3600
}

resource "vault_policy" "bootstrap" {
  name   = "bootstrap"
  policy = file("${path.module}/policies/bootstrap.hcl")
}

resource "vault_audit" "file" {
  type = "file"
  path = "file"

  options = {
    file_path = "/var/log/vault/audit.log"
  }
}
