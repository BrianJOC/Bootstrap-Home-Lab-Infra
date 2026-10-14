# Next release tasks — October 13, 2026

Baseline: `77a9b72`, pushed to `main`; its GitHub source CI passed.
Authority: operator requested planning and delegation to a cheaper model.
Use GPT-6 Luna for bounded implementation and disposable execution, with
primary-agent review. No additional publishing or real-system authority is
implied by this plan.

Measure the experimental release against
[the release contract](../docs/experimental-release-check.md), and retain
[the PRD's 22 criteria](../docs/prd.md#success-criteria) as the broader product
scorecard. Accepted ownership, trust and recovery decisions remain binding.
No manual pass flags, synthetic substitutions for installed identity, or
physical-readiness claims based on QEMU.

| Order | Task | Owner | Completion evidence |
| --- | --- | --- | --- |
| 1A | Repair release-verdict fixtures, fingerprint propagation, and evidence collection; include Authentik unit tests in the recurring task. | Luna implementation agent | Focused tests reject absent, failed, stale or wrong-source evidence; current-SHA binding and resumed-pipeline behavior remain correct; experimental readiness and physical readiness stay separate. |
| 1B | Restore disposable test prerequisites and inspect the existing guest without replacing its snapshot. | Luna execution agent, sole VM/container owner | Docker/integration doctor and guest identity status recorded; saved bootstrap stage, runtime and active processes inspected without exposing secrets. |
| 2 | Review repairs, then run final source and generated-project gates. | Primary reviews; Luna runs checks | Fixture tests, source gates and generated tools/state tests pass with source identity recorded. |
| 3 | Resume bootstrap and run the corrected GitLab lifecycle on the existing fixture. | Same Luna execution agent | Current-source MR plan and protected-main apply; core DNS/host-policy change; trusted TLS/application health; installed Authentik allow/deny; no-change, interrupted reconcile, reviewed deletion and post-deletion rerun pass. |
| 4 | Prove reproducibility on a fresh disposable guest after localized defects are resolved. | Same Luna execution agent | Fresh install and complete current-source lifecycle pass; stable secrets/state and timings recorded in sanitized evidence. Preserve useful prior evidence before replacing disposable state. |
| 5 | Update the established HTML report and first-use instructions against each measured gate. | Primary reviews; Luna prepares documentation | Commit/content fingerprint, fixture identity, pipeline IDs/SHAs, pass/fail and remaining scope are traceable. Experimental release may be called complete only when all its measured gates pass. |

Tasks 1A and 1B may run concurrently; their file ownership is disjoint.
Task 1B prepares infrastructure only: no source sync, bootstrap, merge test or
VM replacement before task 2 passes. One agent alone owns guest/container
mutation. Do not repeat full setup or broad checks after a localized failure.
Commands have finite deadlines; after two repeated failures, diagnose before
another attempt. Use the absolute scoped helper from
[development permissions](../docs/development-permissions.md).

Physical Pi validation is a later externally authorized gate. Backup/recovery,
controller adoption, upgrade policy, observability, wizard convergence and
certificate synchronization remain tracked product work, not silently waived
by this experimental profile. Additional adapters and node expansion are not
part of this tranche.

## Delegation results

- **1A complete, reviewed:** Luna repaired fingerprint propagation and
  seed/Auth/resume evidence collection. Primary review prompted an additional
  fix: invalidate previous acceptance passes before a failed probe or sync can
  return. Unfinished checkpoints remain resumable. Focused validation passed
  17 runner, 4 authorization, 17 merge and 4 Authentik tests.
- **1B complete:** Luna started Docker Desktop and resumed the existing
  snapshot. Integration doctor passes. The guest's latest saved bootstrap log
  is October 4, with k3s failure; the current k3s node/pod summary is healthy.
  See [sanitized readiness evidence](evidence/2026-10-13-disposable-readiness.md).
- **2 complete:** primary review and the full scoped `release-source` command
  passed source doctor, regression fixtures, Go/Ansible/Terraform checks,
  generated project/charts/state lifecycle and the Docker-backed gateway test.
  This is source/component evidence, not combined acceptance.
- **3 delegated to the same Luna execution agent:** run read-only status,
  resume bootstrap, then run the corrected lifecycle only if standup passes.
  Preserve the fixture and capture the first concrete failing gate. Do not
  replace the snapshot or advance to fresh-install proof yet.
- **4–5 pending:** require the combined lifecycle result and primary review.

The operator subsequently authorized pushing these reviewed repairs to `main`
and continuing the remaining disposable acceptance work. Combined acceptance
remains unproven until its measured gates pass.
