# CLAUDE.md

## Project

**bootstrap-home-infra** — Multi-stage bootstrap system for a self-hosted homelab.
Targets a single Ubuntu server (x86 or ARM) and brings up a fully integrated,
internally-networked infrastructure platform driven by IaC and GitOps.

Stack: Ansible (initial deployment), Terraform (GitOps via GitLab), HashiCorp Vault
(secrets), Authentik (SSO), Technitium DNS, GitLab + Runner, MAAS (server provisioning),
PostgreSQL (shared backing service), Cloudflare DNS-01 (TLS), TUI (Go + Bubbletea + Lipgloss).

## Bootstrap Stage Order

Stages have hard dependencies — do not reorder without a clear dependency reason:

1. **DNS** — Technitium DNS (internal name resolution for all services)
2. **TLS** — Cloudflare DNS-01 cert issuance; certs stored in Vault
3. **Vault** — HashiCorp Vault (secrets source of truth, cert distribution)
4. **Authentik** — SSO/OIDC for all services
5. **PostgreSQL** — Shared DB instance (GitLab, Authentik, MAAS)
6. **GitLab + Runner** — GitOps platform; Terraform state backend
7. **MAAS** — Network server provisioning

## Agent Pipeline

```
requirements-analyst → workflow-architect → spec-writer → software-architect → [implementation] → code-reviewer → security-engineer
```

| Agent | Role |
|-------|------|
| `agents-orchestrator` | Coordinates pipeline; enforces stage order; routes work between agents |
| `requirements-analyst` | Discovery and end-state definition; produces PRD before any workflow mapping |
| `workflow-architect` | Maps full workflow trees (happy path, failures, recovery) before any spec |
| `spec-writer` | Produces acceptance criteria, I/O contracts, and test cases from workflow maps |
| `software-architect` | Designs Ansible role structure, Terraform module layout, TUI architecture, ADRs |
| `code-reviewer` | Reviews Ansible, Terraform, bootstrap scripts, and TUI code after implementation |
| `security-engineer` | Reviews secrets handling, Vault config, SSO wiring, TLS, and credential flows |
| `product-manager` | Keeps scope clear; maintains stage status; triages new ideas to current vs. backlog |

## Key Architectural Decisions

- **TUI framework**: Go + Bubbletea + Lipgloss
- **Vault unseal strategy**: Manual Shamir shares — keys written to `vault-init.json`
- **Terraform state migration**: local state during bootstrap → GitLab HTTP backend post-bootstrap
- **Deployment model**: Named k8s clusters (any node provisioner) + bare metal via MAAS/Ansible
- **Scheduler**: k3s — clusters are IaC-defined targets, Kubernetes handles internal scheduling

## Implementation Strategy

### Wave 1 — Parallel (git worktrees)
All four workstreams are independent and dispatched simultaneously:
- `impl/go-skeleton` — Go binary: TUI, config, stage runner interface, SSH client
- `impl/ansible-roles` — All 7 Ansible roles authored together
- `impl/test-infra` — Packer base image, Pebble mock, Cloudflare mock, Molecule scaffolding
- `impl/terraform-modules` — All Terraform modules

### Wave 2 — Sequential validation
One stage at a time, in dependency order. Each stage must pass all spec acceptance
criteria, idempotency check, code-reviewer, and security-engineer before the next begins.

### Wave 3 — Terraform wiring (after Stage 6)
Terraform modules wired to real backends in parallel once GitLab is operational.

## Testing Strategy

| Layer | Tool | Location | When to run |
|-------|------|----------|-------------|
| Ansible role | Molecule (QEMU driver) | `ansible/roles/<name>/` | Every role change; idempotency check (run twice) |
| Bootstrap stage | `just test-stage <name>` | `test/` | Per stage, before merge |
| Full end-to-end | `just test-bootstrap` | `test/` | Pre-release / integration |
| Terraform plan | `terraform plan` | `terraform/` | Every module change |

**Test environment**: QEMU + base `qcow2` (Ubuntu + SSH + ansible user w/ passwordless sudo),
built via Packer (`just build-base-image`). Phase 2: image cached as GitLab CI artifact.

## Permissions

See `.claude/settings.local.json` for allowed commands. Run `just` to see available tasks.

## Notes

- No services are publicly exposed. All TLS via Cloudflare DNS-01 (no inbound required).
- Cloudflare API token is the only externally-sensitive credential — handle with care during bootstrap.
- PostgreSQL is a deployed service, not a dependency of the bootstrap tooling itself.
- Post-bootstrap backlog (monitoring, alerting, additional services) is explicitly out of scope for v1.
