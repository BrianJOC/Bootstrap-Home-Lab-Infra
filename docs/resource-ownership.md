# Resource Ownership and GitOps Handoff

> Status: Accepted target state; source and disposable integration converged
> Last updated: 2026-08-26
> Applies to: bootstrap stages, post-bootstrap GitLab pipelines, recovery, and day-2 changes

## Purpose

Every managed object has exactly one lifecycle owner. Ownership is based on the
kind of resource, not whether the system is currently in bootstrap or day-2
operation. This prevents Ansible and Terraform from creating or reconciling the
same API object and removes imports from the normal clean-bootstrap path.

The Go bootstrap binary coordinates the owners. It does not become a third
configuration-management system.

## Ownership Rules

### Go bootstrap orchestrator

Go owns orchestration and evidence:

- collect and validate operator inputs;
- enforce dependency and stage order;
- run health and precondition checks;
- invoke Ansible for host/runtime work;
- run Terraform init, plan, apply, and state migration;
- display plans and obtain interactive approval;
- pass short-lived bootstrap credentials between stages;
- verify state migration and write the completion record.

Go may create unavoidable one-time bootstrap artifacts, such as a Vault
initialization response or an initial service API token. It must not manage a
long-lived declarative API resource with custom create/update/delete logic when
Terraform owns that resource class.

### Ansible

Ansible owns host and runtime lifecycle:

- operating-system packages and repositories;
- system users, groups, directories, permissions, and mounts;
- Docker, systemd, and service installation;
- service configuration files and protected local runtime secrets;
- database installation, schema/bootstrap users, backup, restore, and migrations;
- service start, stop, reload, upgrade, and health verification;
- one-time product initialization required before an API is usable;
- k3s binary installation and node join operations;
- imperative operations such as certificate issuance and MAAS image import.
- target-local monitoring, retention, paging, and telemetry resource limits.

This ownership continues after handoff. Day-2 Ansible runs are initiated by
reviewed GitLab pipelines rather than directly by an operator.

### Terraform

Terraform owns declarative control-plane and application API objects from the
first moment their service APIs are available:

- internal DNS zones and records;
- Vault mounts, policies, auth methods, and roles;
- Authentik groups, providers, applications, and policy bindings;
- GitLab groups, projects, repository files, variables, branch protections, and
  other durable GitLab API configuration;
- MAAS spaces, fabrics, VLANs, subnets, ranges, and managed machines;
- Kubernetes API objects and Helm releases;
- declarative registrations that connect services to the platform.

Terraform does not install packages, write systemd units, run database
migrations, or install k3s through `remote-exec`.

## Service Ownership Matrix

| Service | Ansible owns | Terraform owns | Bootstrap-only exception |
|---------|---------------|----------------|--------------------------|
| Technitium | Container, storage, resolver integration, admin bootstrap, health | Authoritative zone and DNS records | Initial admin credential |
| TLS / lego | lego install, DNS-01 issuance, renewal, protected certificate files | None in v1 | Go records certificate metadata/material in Vault without treating secret bytes as a Terraform resource |
| Vault | Binary, user, Raft storage, TLS files, systemd, health | Mounts, policies, auth methods, roles, non-secret declarative configuration | Go initializes, saves recovery material, unseals, and holds a short-lived bootstrap token |
| Authentik | Containers, final PostgreSQL runtime configuration, bootstrap admin/token, runtime files, health | Groups, OAuth/OIDC providers, applications, policy bindings | Initial bootstrap token |
| PostgreSQL | Package, configuration, isolated databases/users, credentials, backup/restore tooling | None in v1 | Initial service databases and users are host-runtime resources |
| Backup / Restic | Binary, native artifact hooks, protected password files, mounts, systemd timers, retention/check/restore jobs | None in v1 | Go validates repository identity, presents policy/restore approvals, and records evidence |
| Controller recovery | Stores only the opaque encrypted bundle for scheduled backup; records target-side adoption marker | None in v1 | Go creates and verifies the portable bundle, performs the read-only audit, requires adoption, and coordinates optional credential rotation |
| Upgrade coordination | Packages, component lifecycle, host executor, maintenance timer, health and rollback hooks | Pinned desired versions where a provider owns the resource | Renovate/GitLab propose and audit; Go evaluates deadlines and gates |
| Observability | Kuma/Beszel/health-adapter runtime, retention, resource limits, paging secret files, health | Service definitions supply declarative health-check intent | Go validates paging, thresholds, status, and the local-only outage boundary |
| GitLab | Omnibus package, `gitlab.rb`, certificates, external DB wiring, Runner install/register, health, freeze and exact-version restore | Groups, projects, CI variables/files, branch rules, durable API configuration | Initial root/admin state; Go orchestrates recovery and post-apply Git/state checkpoints |
| MAAS | Snap install, initialization, admin/API credential, image import, health | Space, fabric, VLAN, subnet, ranges, machines | Initial admin and API key |
| k3s | Binary install, server/agent config, node joins, kubeconfig retrieval | Kubernetes objects and Helm releases | Initial cluster credential is written to Vault |

## Bootstrap Terraform Topology

Provider availability follows stage order, so bootstrap uses stage-aligned
Terraform roots rather than one root that initializes every provider at once:

```text
terraform/environments/bootstrap/
  dns/          → state name: bootstrap-dns
  vault/        → state name: bootstrap-vault
  vault-oidc/   → state name: bootstrap-vault-oidc
  vault-gitlab/ → state name: bootstrap-vault-gitlab
  authentik/    → state name: bootstrap-authentik
  gitlab/       → state name: bootstrap-gitlab
  maas/         → state name: bootstrap-maas
```

Each root begins with a local backend. Future cluster and service roots use
their own state names so failure or replacement in one target does not enlarge
the core platform blast radius.

## Stage Execution Contract

For a stage that has both Ansible and Terraform work:

1. Go checks prerequisites and existing health.
2. Go invokes Ansible to install or reconcile the host runtime.
3. Go verifies that the service API is reachable.
4. Go initializes the stage-aligned Terraform root with its local backend.
5. Go runs `terraform plan -out=<run-dir>/<stage>.tfplan`.
6. In interactive mode, the TUI displays the plan summary and requires explicit
   approval before apply. Declining leaves the plan and local state intact and
   stops at a safe boundary.
7. In headless mode, a mutating Terraform apply is permitted only when the
   operator supplied the explicit `--approve-terraform` flag. Headless mode
   without this flag stops before apply with a clear error. Test/CI callers must
   opt in deliberately.
8. Go applies the saved plan and verifies stage postconditions.

No clean-bootstrap stage automatically imports resources. If a Terraform-owned
resource already exists, the stage stops and explains that the target is
brownfield. Brownfield adoption uses a separate, explicit import workflow with
a reviewed resource-to-address map.

## GitLab Handoff Contract

The `bootstrap-gitlab` Terraform root creates the `infrastructure/iac` project
while state is still local. Once GitLab and the project are healthy:

1. Go creates a narrowly scoped Terraform backend credential and stores it in
   Vault.
2. Go prepares the HTTP backend configuration for each bootstrap state.
3. Go runs `terraform init -migrate-state` one state at a time.
4. Go verifies that GitLab can return the migrated state and that its serial and
   lineage match the local state.
5. The local state remains authoritative when migration or verification fails.
6. After remote lineage, serial, and payload equivalence verify, Go atomically
   writes a handoff record and keeps the mode-`0600` local state as a protected
   disaster-recovery snapshot.
7. The handoff record is a hard ownership boundary: all local Terraform apply,
   import, and migration entry points refuse mutation and direct day-2 work to
   the GitLab pipeline.

No import step is required on a clean bootstrap because Terraform created every
Terraform-owned resource and has tracked it since creation.

## Day-2 Contract

After handoff, GitLab is the only supported entry point for changes:

- Terraform pipelines plan and apply declarative API resources.
- Ansible pipelines reconcile packages, files, services, migrations, renewals,
  and k3s node operations.
- Renovate opens pinned-version proposals; configurable review deadlines may
  mark them `approved-by-policy`, but the host maintenance executor still
  enforces backup, compatibility, migration, health, and rollback gates.
- A change requiring both tools uses separate ordered jobs with explicit
  postcondition checks.
- Direct operator invocation is a documented break-glass or recovery action,
  not the routine workflow.

Terraform state can contain values marked `sensitive`; the flag redacts normal
CLI output but does not remove values from state. GitLab backend access must
therefore be narrowly scoped, and secret material should be written through
Vault APIs rather than modeled as Terraform resources when doing so avoids
persisting the secret in state.

## Implementation Convergence Record

This section deliberately separates an accepted decision from its executable
state. An accepted target is not an open design question, but it can still have
implementation or live-proof work.

| Item | State on 2026-08-26 | Remaining work |
|---|---|---|
| GitLab groups/projects/files have one Terraform owner | Implemented and disposable-integration verified | Physical-homelab acceptance only |
| Saved plans, interactive approval, and headless opt-in | Implemented and unit-tested | Optional interactive-TUI visual acceptance |
| Verified GitLab HTTP state migration with rollback copy | Implemented and disposable-integration verified for every bootstrap state | Destructive full GitLab-loss drill remains |
| GitLab imports removed from clean bootstrap | Implemented | Brownfield import remains a separate future workflow |
| k3s host installation moved out of Terraform | Implemented and disposable-integration verified | Multi-node physical acceptance remains |
| Authentik OIDC objects have one Terraform owner | Converged and disposable-integration verified | None at source level |
| Technitium zone/records have one owner | Converged and disposable-integration verified | None at source level |
| Vault mounts/policies/auth roles have one owner | Converged and disposable-integration verified | None at source level |
| MAAS network API objects have one Terraform owner | Converged and disposable-integration verified | Physical VLAN/DHCP acceptance remains deliberate |
| Stage-aligned roots replace the monolithic bootstrap root | Complete | Keep split roots and handoff guards covered by CI |
