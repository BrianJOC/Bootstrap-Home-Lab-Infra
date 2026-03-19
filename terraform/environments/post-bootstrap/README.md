# Post-Bootstrap Terraform Environment

This environment uses the GitLab HTTP backend (SC-14).

## Migration from bootstrap environment

After GitLab is operational (Stage 6 complete) and all bootstrap stages have run:

1. Retrieve the Terraform project access token from Vault:
   ```
   vault kv get -field=token secret/data/bootstrap/gitlab/terraform-token
   ```

2. Set the token as an environment variable (never write it to backend.tf):
   ```
   export TF_HTTP_PASSWORD=<token-from-vault>
   ```

3. Open `backend.tf` in this directory and uncomment the `terraform { backend "http" { ... } }` block.
   Fill in the actual GitLab domain and project ID from the bootstrap output.

4. Initialize and migrate state:
   ```
   just migrate-tf-state
   ```
   This runs `terraform init -migrate-state` and prompts for confirmation.

5. Verify the migration succeeded:
   ```
   terraform state list
   ```
   All resources from the bootstrap environment should appear.

6. Confirm the GitLab HTTP backend holds the state:
   ```
   curl -s -H "PRIVATE-TOKEN: $TF_HTTP_PASSWORD" \
     "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/homelab" | jq .
   ```

7. Only after confirming the above, delete local state:
   ```
   rm terraform/environments/bootstrap/terraform.tfstate
   rm terraform/environments/bootstrap/terraform.tfstate.backup
   ```

**Do NOT delete local state until migration is confirmed successful.**

## Day-2 usage

After migration, all Terraform changes go through the `infrastructure/iac` GitLab project
via merge request. The bootstrap tool is no longer needed for infrastructure management.

See: `infrastructure/iac/.gitlab-ci.yml` for the pipeline definition.
