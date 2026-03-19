# Local backend used during bootstrap stages (before GitLab is operational).
# After Stage 6 completes and all bootstrap stages are done, run:
#   just migrate-tf-state
# to migrate this state to the GitLab HTTP backend (SC-14 completion condition).

terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}
