terraform {
  required_version = ">= 1.6"
  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "~> 16.0"
    }
  }
}

provider "gitlab" {
  base_url = var.gitlab_url
  token    = var.gitlab_token
}

# ── Top-level Groups ──────────────────────────────────────────────────────────

resource "gitlab_group" "infrastructure" {
  name             = "infrastructure"
  path             = "infrastructure"
  description      = "Infrastructure management — Terraform, MAAS, cluster definitions"
  visibility_level = "private"
}

resource "gitlab_group" "services" {
  name             = "services"
  path             = "services"
  description      = "Service definitions and deployment configurations"
  visibility_level = "private"
}

# ── IaC Project (Terraform state backend home) ────────────────────────────────

resource "gitlab_project" "iac" {
  name             = "iac"
  path             = "iac"
  description      = "Infrastructure as Code — Terraform configurations, service definitions, cluster definitions"
  namespace_id     = gitlab_group.infrastructure.id
  visibility_level = "private"

  # Initialize with a README so the repo is not empty (required for HTTP backend)
  initialize_with_readme = true

  # Allow Terraform state management via GitLab HTTP backend
  # This is the target for terraform init -migrate-state (SC-14)
}

# ── Project-level CI/CD Variables ─────────────────────────────────────────────

resource "gitlab_project_variable" "vault_addr" {
  project   = gitlab_project.iac.id
  key       = "VAULT_ADDR"
  value     = var.vault_address
  protected = false
  masked    = false
}

resource "gitlab_project_variable" "vault_auth_path" {
  project   = gitlab_project.iac.id
  key       = "VAULT_AUTH_PATH"
  value     = var.vault_jwt_auth_path
  protected = false
  masked    = false
}

resource "gitlab_project_variable" "vault_role" {
  project   = gitlab_project.iac.id
  key       = "VAULT_ROLE"
  value     = "gitlab-ci"
  protected = false
  masked    = false
}

# ── GitLab CI Pipeline Configuration ─────────────────────────────────────────

resource "gitlab_project_file" "ci_config" {
  project        = gitlab_project.iac.id
  file_path      = ".gitlab-ci.yml"
  branch         = "main"
  content        = base64encode(file("${path.module}/templates/gitlab-ci.yml"))
  commit_message = "ci: add bootstrap pipeline configuration"
  author_name    = "bootstrap"
  author_email   = "bootstrap@${var.internal_domain}"
}

# ── Terraform backend example (committed to repo) ─────────────────────────────

resource "gitlab_project_file" "backend_example" {
  project        = gitlab_project.iac.id
  file_path      = "terraform/backend.tf.example"
  branch         = "main"
  content        = base64encode(templatefile("${path.module}/templates/backend.tf.example.tpl", {
    gitlab_url  = var.gitlab_url
    project_id  = gitlab_project.iac.id
    domain      = var.internal_domain
  }))
  commit_message = "docs: add Terraform HTTP backend example"
  author_name    = "bootstrap"
  author_email   = "bootstrap@${var.internal_domain}"

  depends_on = [gitlab_project_file.ci_config]
}
