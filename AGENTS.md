# Repository Working Agreement

This file records the operator's standing instructions for implementation work
in this repository. Read it together with
`docs/implementation-working-agreement.md` and the accepted decision documents
linked there.

## Default authority

Agents may, without asking again:

- inspect and edit files inside this repository;
- implement accepted behavior and fix defects;
- add or update unit, integration, QEMU, Molecule, and static-validation tests;
- format code and run non-destructive source checks;
- create, start, stop, and replace repository-owned disposable test VMs,
  containers, mocks, temporary state, caches, and test configuration;
- download declared development dependencies when the execution environment
  requests the required network approval;
- update documentation, decision status, evidence, and the repository review
  after implementation changes;
- make reasonable implementation choices that preserve the accepted
  architecture and defaults.

Do not ask the operator to repeat a decision already recorded in the accepted
documents. If implementation reveals a contradiction, cite both documents,
choose the safer reversible interpretation when possible, and record the
resolution.

## Boundaries that still require explicit authority

- Mutating a real homelab host, UniFi network, TrueNAS system, Cloudflare zone,
  GitLab instance, Vault instance, or other non-disposable external system.
- Sending messages, opening pull requests, pushing branches, merging, publishing,
  or changing remote repository settings.
- Destructive or irreversible recovery, credential rotation, data deletion, or
  adoption of an existing server.
- Changing an accepted product decision rather than implementing it.
- Using real secrets when mocks or synthetic credentials can perform the test.

Read-only inspection of an explicitly in-scope external target is allowed when
credentials and connectivity are already available, but report what was
inspected and do not turn a read-only review into a mutation.

## Validation order

For recurring checks, logs, and disposable guest operations, use the scoped
entry point documented in `docs/development-permissions.md`. Prefer its absolute
invocation over shell wrappers, per-file SCP approvals, or new broad rules.
This does not expand authority over real systems or publishing.

1. Run `scripts/dev-doctor.sh source`.
2. Run conflict, formatting, Go test/build/vet, Ansible syntax/lint, and
   Terraform format/validate checks.
3. Add focused regression tests for the changed behavior.
4. When appropriate, run `scripts/dev-doctor.sh integration`, then use the
   repository's disposable QEMU and mock workflows.
5. Run fresh-install, no-change rerun, and interrupted-resume cases before
   describing a workflow as proven.
6. Preserve logs/evidence without storing credentials or secret values.

Use writable temporary paths under `/tmp` for Go, Ansible, and Terraform caches
when the execution environment restricts home-directory writes.

Use the repository's Hermit environment for managed command-line tools. Python
test packages are the one exception only in packaging form: Hermit owns the
Python and `uv` executables, and Hermit's `uv` installs the pinned packages into
the ignored `.venv` from `requirements-dev.lock`. Do not use a global Python,
global `uv`, Homebrew, or a second tool manager for repository setup.

## Report presentation

Keep `reports/repository-review-2026-07-26.html` in its established navy,
cream, teal, and serif-heading theme. Update findings and evidence in place; do
not redesign it unless the operator explicitly requests a redesign.
