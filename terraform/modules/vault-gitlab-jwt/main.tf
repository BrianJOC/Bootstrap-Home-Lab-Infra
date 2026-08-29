terraform {
  required_version = ">= 1.6"
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "3.25.0"
    }
  }
}

resource "vault_jwt_auth_backend" "gitlab" {
  path               = "jwt"
  type               = "jwt"
  description        = "JWT auth for GitLab CI pipeline secret injection"
  oidc_discovery_url = "https://gitlab.${var.internal_domain}"
  bound_issuer       = "https://gitlab.${var.internal_domain}"
}

resource "vault_policy" "gitlab_ci" {
  name   = "gitlab-ci"
  policy = file("${path.module}/gitlab-ci.hcl")
}

resource "vault_jwt_auth_backend_role" "gitlab_ci" {
  backend        = vault_jwt_auth_backend.gitlab.path
  role_name      = "gitlab-ci"
  token_policies = [vault_policy.gitlab_ci.name]
  token_ttl      = 3600

  bound_audiences = ["https://vault.${var.internal_domain}:8200"]
  user_claim      = "sub"
  role_type       = "jwt"
}
