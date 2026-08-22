# Implementation and Testing Working Agreement

> Status: Accepted standing instructions
> Last updated: 2026-07-29

## Purpose

This document lets implementation continue without repeatedly asking the
administrator to restate settled product choices. It separates routine local
engineering authority from changes to real infrastructure or accepted product
decisions.

## Standing implementation defaults

- Continue from the accepted architecture; do not reopen decisions merely
  because their implementation or live proof is incomplete.
- Prefer secure, reversible, idempotent behavior and explicit degraded states.
- Preserve the single-administrator-laptop operating model.
- Treat the target Ubuntu system as constrained compute.
- Treat TrueNAS and optional USB devices as storage only.
- Use trial mode when no backup destination exists.
- Preserve controller and Vault recovery material across safe reruns.
- Keep manual Vault unseal as the v1 default.
- Keep Technitium homelab-scoped and MAAS DHCP disabled unless deliberately
  enabled for the validated lab VLAN.
- Keep application upgrades review-first, with configurable deadlines and
  maintenance-window policy approval.
- Keep monitoring target-local, bounded, short-retention, and optionally paged.
- Keep Ansible as host/runtime owner, Terraform as durable API-object owner,
  and Go as orchestrator.

The detailed source-of-truth decisions are:

- [Resource ownership](resource-ownership.md)
- [SSH host trust](ssh-host-trust.md)
- [Database architecture](database-architecture.md)
- [Network cutover](network-cutover.md)
- [Backup and recovery](backup-recovery.md)
- [Certificate renewal](certificate-renewal.md)
- [Upgrade policy](upgrade-policy.md)
- [Observability and paging](observability.md)
- [Reboot recovery](reboot-recovery.md)
- [GitLab disaster recovery](gitlab-disaster-recovery.md)
- [Controller recovery](controller-recovery.md)

## Work that can proceed autonomously

The implementation agent may edit code and documentation, add tests, run all
source checks, install repository-declared developer dependencies, and operate
repository-owned disposable VMs, containers, mock APIs, state, and test data.
Failures should normally be diagnosed and fixed rather than returned as a
question.

The agent may choose minor implementation details when they do not alter the
accepted product behavior. Those choices should be captured in code comments,
tests, or this document when they are likely to matter again.

## Actions that remain deliberate

Explicit administrator authority is still required before:

- changing any real target, router, NAS, public DNS zone, or deployed service;
- using production credentials in a test;
- rotating or revoking credentials;
- restoring over live data or adopting an existing machine;
- pushing, publishing, opening or merging a pull request, or modifying remote
  repository settings;
- replacing an accepted architectural or operating decision.

Tool-enforced approval prompts for network access, package download, browser
rendering, or local virtualization are execution-environment permissions, not
requests to reconsider the product design.

## Tooling baseline

The supported local baseline is:

- Go from `go.mod`;
- Terraform 1.6 or newer;
- the checked-in Hermit environment for repository command-line tools;
- Hermit-pinned Python 3.12 and `uv`, with `uv` installing the Python packages
  locked in `requirements-dev.lock` into the ignored `.venv`;
- Ansible collections pinned in `ansible/requirements.yml`;
- Docker for mock APIs and container-backed role tests;
- QEMU with HVF on Apple Silicon, or TCG where acceleration is unavailable;
- Packer for the disposable Ubuntu base image;
- the repository-local `just` task runner.

Do not bootstrap this repository with a global Python or global `uv`. Hermit is
the toolchain entry point even though Python packages themselves live in the
local virtual environment.

Edit `requirements-dev.in` for an intentional tool change and run
`just dev-lock`. Normal setup consumes the committed hashed lock and does not
re-resolve versions.

Run:

```bash
just dev-setup
just doctor
just doctor-integration
```

`doctor` covers source work. `doctor-integration` also checks the Docker daemon,
Molecule, QEMU, Packer, test SSH key, and QEMU base image.

## Evidence vocabulary

- **Decision accepted:** the intended behavior is settled.
- **Source implemented:** executable code and focused tests exist.
- **Statically verified:** relevant source gates pass.
- **Integration verified:** disposable VM/container/API tests pass.
- **Live proven:** the intended external system passed the documented drill.

Never describe an accepted decision as an open design gap. Never describe
source implementation as live proof.
