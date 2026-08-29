terraform {
  required_version = ">= 1.6"
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "3.25.0"
    }
  }
}

resource "vault_jwt_auth_backend" "oidc" {
  path               = "oidc"
  type               = "oidc"
  oidc_discovery_url = "https://authentik.${var.internal_domain}/application/o/vault/"
  oidc_client_id     = var.oidc_client_id
  oidc_client_secret = var.oidc_client_secret
  description        = "OIDC auth via Authentik"

  tune {
    listing_visibility = "unauth"
    default_lease_ttl  = "768h"
    max_lease_ttl      = "768h"
    token_type         = "default-service"
  }
}

resource "vault_jwt_auth_backend_role" "default" {
  backend        = vault_jwt_auth_backend.oidc.path
  role_name      = "default"
  token_policies = ["default"]

  bound_audiences       = [var.oidc_client_id]
  allowed_redirect_uris = ["https://vault.${var.internal_domain}:8200/ui/vault/auth/oidc/oidc/callback"]
  user_claim            = "sub"
  role_type             = "oidc"
}
