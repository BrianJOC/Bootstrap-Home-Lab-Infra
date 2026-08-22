# Terraform

Infrastructure as Code for bootstrap-home-infra.

## Structure

```
terraform/
  modules/
    dns-records/        Technitium DNS record management (null_resource + curl)
    vault-config/       Vault secrets engines, policies, and auth methods
    authentik-config/   Authentik groups, OIDC providers, and applications
    gitlab-config/      GitLab groups, projects, and CI/CD variables
    maas-config/        MAAS fabric, VLAN, and subnet with DHCP
    k8s-cluster/        k3s cluster installation via remote-exec
    helm-release/       Generic Helm chart deployment to a named cluster
  environments/
    bootstrap/          Local backend — used during bootstrap stages 1–8
    post-bootstrap/     GitLab HTTP backend — activated after SC-14 migration
```

## Environments

### `bootstrap/`

Used during bootstrap stages before GitLab is operational. State stored locally.

```bash
just tf-init    # terraform init
just tf-plan    # terraform plan
just tf-apply   # terraform apply
```

All sensitive variables (tokens, API keys) are passed via `TF_VAR_*` environment
variables, never written to `terraform.tfvars`.

Copy `terraform/environments/bootstrap/terraform.tfvars.example` to
`terraform.tfvars` and fill in non-sensitive structural values.

### `post-bootstrap/`

Activated after Stage 6 (GitLab) is complete and state is migrated via SC-14.
See `terraform/environments/post-bootstrap/README.md` for migration instructions.

```bash
just migrate-tf-state
```

## Modules

### `dns-records`

Manages Technitium DNS records using `null_resource` with `local-exec` curl calls.
No official Terraform provider for Technitium exists; the curl-based approach is
pragmatic and fully idempotent (delete-then-recreate on every apply).

**Providers:** `hashicorp/null`

### `vault-config`

Manages Vault post-init configuration: KV v2 + PKI mounts, bootstrap and gitlab-ci
policies, OIDC and JWT auth methods, and TLS certificate storage.

**Providers:** `hashicorp/vault ~> 3.0`

### `authentik-config`

Manages Authentik groups, OAuth2/OIDC providers, applications, and policy bindings
(group-based access control per application).

**Providers:** `goauthentik/authentik ~> 2024.2`

### `gitlab-config`

Manages GitLab top-level groups (`infrastructure`, `services`), the `infrastructure/iac`
project, project-level CI/CD variables, and committed files (`.gitlab-ci.yml`,
`terraform/backend.tf.example`).

**Providers:** `gitlabhq/gitlab ~> 16.0`

### `maas-config`

Manages MAAS network topology: space, fabric, lab VLAN, and lab subnet with DHCP
dynamic range. Upstream DNS is set to the Technitium server IP.

**Providers:** `canonical/maas ~> 2.8.0`

### `k8s-cluster`

Installs k3s on named nodes via `null_resource` + `remote-exec`. Node provisioning
is decoupled — this module only requires SSH reachability. First control-plane
bootstraps with `--cluster-init`; additional control-planes and workers join via
the node-token from the primary.

**Providers:** `hashicorp/null`

### `helm-release`

Deploys a Helm chart to a named k3s cluster using a kubeconfig sourced from Vault
(or a local path). Supports values files and `--set` overrides.

**Providers:** `hashicorp/helm ~> 2.12`

## State Migration (SC-14)

After all bootstrap stages complete, local `.tfstate` is migrated to the GitLab
HTTP backend. The migration is non-destructive: local state is only deleted after
the GitLab backend confirms readability. See `environments/post-bootstrap/README.md`.
