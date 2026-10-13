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
  bound_claims = {
    project_path     = "infrastructure/iac"
    job_project_path = "infrastructure/iac"
  }
}

resource "vault_policy" "gitops_plan" {
  name   = "gitlab-plan"
  policy = file("${path.module}/gitops-plan.hcl")
}

resource "vault_policy" "gitops_apply" {
  name   = "gitlab-apply"
  policy = "${file("${path.module}/gitops-plan.hcl")}\n${file("${path.module}/gitops-apply.hcl")}"
}

resource "vault_jwt_auth_backend_role" "gitops_plan" {
  backend         = vault_jwt_auth_backend.gitlab.path
  role_name       = "gitlab-plan"
  role_type       = "jwt"
  user_claim      = "sub"
  bound_audiences = ["https://vault.${var.internal_domain}:8200"]
  bound_claims = {
    project_path     = "infrastructure/iac"
    job_project_path = "infrastructure/iac"
  }
  token_policies = [vault_policy.gitops_plan.name]
  token_ttl      = 1800
  token_max_ttl  = 3600
}

resource "vault_jwt_auth_backend_role" "gitops_apply" {
  backend         = vault_jwt_auth_backend.gitlab.path
  role_name       = "gitlab-apply"
  role_type       = "jwt"
  user_claim      = "sub"
  bound_audiences = ["https://vault.${var.internal_domain}:8200"]
  bound_claims = {
    project_path     = "infrastructure/iac"
    job_project_path = "infrastructure/iac"
    ref              = "main"
    ref_type         = "branch"
    ref_protected    = "true"
  }
  token_policies = [vault_policy.gitops_apply.name]
  token_ttl      = 1800
  token_max_ttl  = 3600
}
