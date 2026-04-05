# Post-Bootstrap: State Migration to GitLab HTTP Backend (SC-14)

This directory contains the GitLab HTTP backend configuration that the bootstrap
environment's state migrates to once GitLab is operational.

## Prerequisites

- Stage 6 complete (GitLab running, `infrastructure/iac` project exists)
- All 7 bootstrap stages validated
- Vault is unsealed and accessible

## Step 1 — Create a Terraform project access token in GitLab

Via the GitLab UI or API, create a project access token for `infrastructure/iac`
with `api` scope. Store it in Vault:

```sh
vault kv put secret/bootstrap/gitlab/terraform-token token=<token>
```

## Step 2 — Export the token

```sh
export TF_HTTP_PASSWORD=$(vault kv get -field=token secret/bootstrap/gitlab/terraform-token)
```

## Step 3 — Update bootstrap/backend.tf to the GitLab HTTP backend

Replace the `local` backend block in `terraform/environments/bootstrap/backend.tf`
with the content from this directory's `backend.tf` (uncommented, with real values filled in):

```hcl
terraform {
  backend "http" {
    address        = "https://gitlab.<domain>/api/v4/projects/<project-id>/terraform/state/homelab"
    lock_address   = "https://gitlab.<domain>/api/v4/projects/<project-id>/terraform/state/homelab/lock"
    unlock_address = "https://gitlab.<domain>/api/v4/projects/<project-id>/terraform/state/homelab/lock"
    username       = "bootstrap"
    lock_method    = "POST"
    unlock_method  = "DELETE"
    retry_wait_min = 5
  }
}
```

Get the project ID:
```sh
curl -sf -H "PRIVATE-TOKEN: $TF_HTTP_PASSWORD" \
  "https://gitlab.<domain>/api/v4/projects/infrastructure%2Fiac" | jq '.id'
```

## Step 4 — Migrate state

```sh
just migrate-tf-state
```

This runs `terraform init -migrate-state` in the bootstrap environment and prompts
for confirmation before moving state.

## Step 5 — Import Ansible-created GitLab resources

The Ansible `gitlab_install` role creates the `infrastructure` group, `services` group,
and `infrastructure/iac` project during bootstrap. Terraform must import these into
state before it can manage them — otherwise the first `terraform apply` will fail with
409 Conflict errors.

```sh
just tf-import-gitlab \
  GITLAB_URL=https://gitlab.<domain> \
  GITLAB_TOKEN=$TF_HTTP_PASSWORD
```

## Step 6 — Verify

```sh
cd terraform/environments/bootstrap
terraform state list   # all resources should appear
terraform plan         # should show no changes (or only expected diffs)
```

## Step 7 — Confirm GitLab holds the state

```sh
curl -sf -H "PRIVATE-TOKEN: $TF_HTTP_PASSWORD" \
  "https://gitlab.<domain>/api/v4/projects/<project-id>/terraform/state/homelab" | jq .serial
```

If this returns a serial number, migration succeeded.

## Step 8 — Remove local state files

Only after confirming migration:
```sh
rm terraform/environments/bootstrap/terraform.tfstate
rm terraform/environments/bootstrap/terraform.tfstate.backup
```

**Do NOT delete local state until migration is confirmed.**

## Day-2 operations

After migration, all infrastructure changes go through GitLab merge requests.
The `infrastructure/iac` project CI pipeline (`.gitlab-ci.yml`) runs:
- `terraform validate` on MR open
- `terraform plan` on MR open
- `terraform apply` on merge to main (manual gate)

Secrets are injected from Vault via the JWT auth backend using GitLab CI OIDC tokens.
