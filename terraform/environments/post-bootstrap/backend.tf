# GitLab HTTP backend — used after SC-14 migration from bootstrap local state.
#
# Uncomment and configure after running: just migrate-tf-state
#
# Prerequisites:
#   1. GitLab is operational (Stage 6 complete)
#   2. infrastructure/iac project exists in GitLab
#   3. Terraform project access token is in Vault at:
#        secret/data/bootstrap/gitlab/terraform-token
#
# The password (project access token) must NOT appear in this file.
# It is injected at runtime via the TF_HTTP_PASSWORD environment variable,
# which the Go bootstrap runner sources from Vault.

# terraform {
#   backend "http" {
#     address        = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/homelab"
#     lock_address   = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/homelab/lock"
#     unlock_address = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/homelab/lock"
#     username       = "bootstrap"
#     # password sourced from TF_HTTP_PASSWORD environment variable
#     lock_method    = "POST"
#     unlock_method  = "DELETE"
#     retry_wait_min = 5
#   }
# }
