terraform {
  required_version = ">= 1.6"
  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "19.2.1"
    }
  }
}

provider "gitlab" {
  base_url = var.gitlab_url
  token    = var.gitlab_token
  insecure = var.gitlab_insecure
}

module "gitlab" {
  source          = "../../../modules/gitlab-config"
  gitlab_url      = var.gitlab_url
  vault_address   = var.vault_address
  internal_domain = var.internal_domain
  maas_api_url    = var.maas_api_url
  iac_source_dir  = var.iac_source_dir
}

variable "gitlab_url" { type = string }
variable "gitlab_token" {
  type      = string
  sensitive = true
}
variable "gitlab_insecure" {
  type    = bool
  default = false
}
variable "vault_address" { type = string }
variable "internal_domain" { type = string }
variable "maas_api_url" { type = string }
variable "iac_source_dir" {
  type    = string
  default = ""
}

output "iac_project_id" { value = module.gitlab.iac_project_id }
