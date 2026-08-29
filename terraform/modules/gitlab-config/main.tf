terraform {
  required_version = ">= 1.6"
  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "19.2.1"
    }
  }
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

# GitLab CE represents the group Owner as access level 50 but evaluates
# protected project branches using project roles. Restrict direct bootstrap
# pushes to the instance administrator (level 60); all routine changes still
# arrive through merge requests and the reviewed pipeline.
resource "gitlab_branch_protection" "main" {
  project            = gitlab_project.iac.id
  branch             = "main"
  push_access_level  = "admin"
  merge_access_level = "maintainer"
  allow_force_push   = false
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

resource "gitlab_project_variable" "maas_api_url" {
  project   = gitlab_project.iac.id
  key       = "MAAS_API_URL"
  value     = var.maas_api_url
  protected = false
  masked    = false
}

# ── GitLab CI Pipeline Configuration ─────────────────────────────────────────

resource "gitlab_repository_file" "ci_config" {
  project        = gitlab_project.iac.id
  file_path      = ".gitlab-ci.yml"
  branch         = "main"
  content        = base64encode(file("${path.module}/templates/gitlab-ci.yml"))
  encoding       = "base64"
  commit_message = "ci: add bootstrap pipeline configuration [skip ci]"

  depends_on = [gitlab_branch_protection.main]
}

# ── Terraform backend example (committed to repo) ─────────────────────────────

resource "gitlab_repository_file" "backend_example" {
  project   = gitlab_project.iac.id
  file_path = "terraform/backend.tf.example"
  branch    = "main"
  content = base64encode(templatefile("${path.module}/templates/backend.tf.example.tpl", {
    gitlab_url = var.gitlab_url
    project_id = gitlab_project.iac.id
    domain     = var.internal_domain
  }))
  encoding       = "base64"
  commit_message = "docs: add Terraform HTTP backend example [skip ci]"

  depends_on = [gitlab_repository_file.ci_config]
}

resource "gitlab_repository_file" "terraform_versions" {
  project        = gitlab_project.iac.id
  file_path      = "terraform/versions.tf"
  branch         = "main"
  content        = base64encode(file("${path.module}/templates/versions.tf"))
  encoding       = "base64"
  commit_message = "build: add the GitLab HTTP state root [skip ci]"

  depends_on = [gitlab_repository_file.backend_example]
}

resource "gitlab_repository_file" "maas_machines" {
  project        = gitlab_project.iac.id
  file_path      = "terraform/maas-machines.tf"
  branch         = "main"
  content        = base64encode(file("${path.module}/templates/maas-machines.tf"))
  encoding       = "base64"
  commit_message = "build: manage reviewed MAAS node definitions [skip ci]"

  depends_on = [gitlab_repository_file.terraform_versions]
}

resource "gitlab_repository_file" "cluster_directory" {
  project        = gitlab_project.iac.id
  file_path      = "infrastructure/clusters/.gitkeep"
  branch         = "main"
  content        = ""
  encoding       = "text"
  commit_message = "chore: add cluster definition directory [skip ci]"

  depends_on = [gitlab_repository_file.maas_machines]
}

resource "gitlab_repository_file" "node_directory" {
  project        = gitlab_project.iac.id
  file_path      = "infrastructure/nodes/.gitkeep"
  branch         = "main"
  content        = ""
  encoding       = "text"
  commit_message = "chore: add node definition directory [skip ci]"

  depends_on = [gitlab_repository_file.cluster_directory]
}
