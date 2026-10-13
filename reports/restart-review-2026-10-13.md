# Repository restart review — October 13, 2026

The visual dashboard remains [the repository review](repository-review-2026-07-26.html).
This review refreshes the October 4 handoff without claiming a completed release.

## Where we left off

GitHub's default branch is `main`; no local or remote `master` exists. After
fetching, local HEAD and `origin/main` were both
`097a0c26bdc3030a98648cff5017f48345814bc0`. GitHub reported its last repository
push at **2026-08-29 13:45:26 UTC** (09:45:26 EDT), 45 days before this review.
The operator authorized pushing the latest work; the pending GitOps source and
this review are being preserved on the existing default branch.

The uncommitted implementation adds the final GitOps stage, a generated GitLab
management project, host reconciliation, service charts, Vault CI policies,
web-service Terraform, and disposable acceptance automation. It also contains
k3s download retries, current-SHA MR-plan checks, failed-main-pipeline retry,
installed-Authentik login tests, interrupted-reconcile tests, and a seed-source
comparison. Those repairs supersede parts of the October 4 plan at the source
level. Their existence does not establish a successful combined journey.

The September 28 bootstrap timeout at k3s remains the last guest run recorded
in the handoff documents. Today's guest probe could not connect to port 2222;
it did not inspect the guest filesystem or establish its current stage.

## Fresh validation

| Check | Result |
| --- | --- |
| Source doctor | PASS |
| Conflict/format, Go tests/build/vet, Ansible syntax/lint, Terraform roots/modules/generated-IaC validation (`lint-all`) | PASS |
| Generated GitLab project, Helm charts, real Terraform state lifecycle | PASS |
| Release fixture tests | FAIL: one assertion failure and two errors in `test-release-check.py` |
| Integration doctor | BLOCKED: Docker daemon unavailable; base image and disposable SSH key present |
| Read-only release status | NO: fixed guest SSH connection unavailable |
| Candidate-file credential pattern scan | No private-key, AWS access-key, GitHub-token or GitLab-token pattern matches; not an exhaustive security audit |

Detailed logs remain private under `/tmp` and ignored `logs/release-check/`.
No raw runtime logs, credentials, controller configuration or Terraform state
are included in the snapshot. GitHub CI results for the new push must be checked
separately; local source passes do not imply a release acceptance pass.

## What remains, in order

1. Finish release-runner integration. Its verdict now separates experimental
   QEMU readiness from physical Pi readiness, but tests still expect the old
   missing-test contract; a standup test fake lacks `state`. `verify()` also
   invokes the merge fixture without its required `--source-fingerprint`.
   Wire and validate source-bound Authentik, interrupted-resume and seeded-source
   evidence into their named gates; they are declared but never recorded by the
   runner. Include the Authentik unit fixture in the recurring test task.
2. Start Docker and resume the existing disposable VM. Inspect its saved stage
   and logs before rerunning bootstrap. Keep checksum verification and test-only
   credentials; do not rebuild the guest for each localized failure.
3. Prove the current-source GitLab journey: current-SHA MR plan, protected-main
   apply, core DNS/host-policy change, service DNS/TLS/Helm/health, actual Authentik
   allow/deny, unchanged rerun, interrupted reconciliation, reviewed removal,
   and post-removal rerun. Record commit/pipeline/source identity and timings.
4. Once stable, run the entire journey from a fresh guest and archive sanitized
   evidence. A resumed old guest cannot supply fresh-install proof.
5. Physical Raspberry Pi capacity/kernel/storage/CI proof remains separate and
   requires an explicitly authorized hardware target. QEMU cannot certify it.

Broader accepted-product follow-ups remain visible as F2–F8 in the HTML:
backup completeness, recovery checkpoints, controller recovery/adoption,
maintenance upgrades, observability/paging, wizard/lifecycle convergence, and
automatic certificate synchronization. They were not fully reassessed today.
Node expansion and additional service adapters remain outside the immediate
experimental web-service release. No real homelab system was inspected or changed.
