terraform {
  required_version = ">= 1.6"
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "~> 3.0"
    }
  }
}

provider "vault" {
  address         = var.vault_address
  token           = var.vault_token
  skip_tls_verify = false
}

# ── Secrets Engines ────────────────────────────────────────────────────────────

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
  default_lease_ttl_seconds = 86400           # 24 hours
  max_lease_ttl_seconds     = 365 * 24 * 3600 # 1 year
}

# ── Policies ──────────────────────────────────────────────────────────────────

resource "vault_policy" "bootstrap" {
  name   = "bootstrap"
  policy = file("${path.module}/policies/bootstrap.hcl")
}

resource "vault_policy" "gitlab_ci" {
  name   = "gitlab-ci"
  policy = file("${path.module}/policies/gitlab-ci.hcl")
}

# ── Auth Methods ──────────────────────────────────────────────────────────────

# OIDC auth method — discovery URL points at Authentik; fully configured in Stage 4
resource "vault_jwt_auth_backend" "oidc" {
  path               = "oidc"
  type               = "oidc"
  oidc_discovery_url = "https://authentik.${var.internal_domain}/application/o/vault/"
  description        = "OIDC auth via Authentik"

  tune {
    listing_visibility = "unauth"
  }
}

# JWT auth method — used by GitLab CI pipelines for Vault secret injection.
# GitLab publishes its JWKS at /-/jwks; Vault uses this to validate CI token signatures.
resource "vault_jwt_auth_backend" "gitlab_jwt" {
  path        = "jwt"
  type        = "jwt"
  description = "JWT auth for GitLab CI pipeline secret injection"

  jwks_url      = "https://gitlab.${var.internal_domain}/-/jwks"
  bound_issuer  = "https://gitlab.${var.internal_domain}"

  tune {
    listing_visibility = "hidden"
  }
}

# GitLab CI role: can read service and infrastructure secrets
resource "vault_jwt_auth_backend_role" "gitlab_ci" {
  backend        = vault_jwt_auth_backend.gitlab_jwt.path
  role_name      = "gitlab-ci"
  token_policies = [vault_policy.gitlab_ci.name]
  token_ttl      = 3600

  bound_audiences = ["https://gitlab.${var.internal_domain}"]
  user_claim      = "sub"
  role_type       = "jwt"
}

# ── TLS Certificate Storage ───────────────────────────────────────────────────

resource "vault_generic_secret" "tls_wildcard" {
  path = "secret/data/bootstrap/tls/wildcard"

  data_json = jsonencode({
    cert   = file(var.cert_path)
    key    = file(var.key_path)
    domain = var.internal_domain
  })

  depends_on = [vault_mount.kv]
}

# ── Audit Log ─────────────────────────────────────────────────────────────────

resource "vault_audit" "file" {
  type = "file"
  path = "file"

  options = {
    file_path = "/var/log/vault/audit.log"
  }
}
