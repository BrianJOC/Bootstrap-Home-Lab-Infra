# What remains before this tool is usable

> Historical October 4 plan. The [October 13 restart review](restart-review-2026-10-13.md)
> supersedes its current blocker list: download and pipeline repairs now exist,
> while release-runner wiring, failing fixtures and unavailable disposable
> infrastructure are the immediate next steps. Combined acceptance remains unproven.

Assessed October 4, 2026. Scope: the two requested outcomes—GitLab takes over
core configuration and host runtime, and a reviewed web-service declaration
produces an authenticated, working service. This is a completion plan, not a
claim that the release has passed.

## Verdict

The implementation has substantial component evidence, but we have not yet
proved the complete workflow you would rely on. There is no current evidence
that calls for an architectural rewrite. There is also insufficient evidence
to call this ready for your real infrastructure.

The delay includes avoidable execution problems: repeated setup, unfinished
acceptance automation, and a report that was not updated after the last run.
The priority now is to finish one bounded operational workflow and record its
results, rather than broaden the feature set or repeat general assessments.

## What actually happened

The last disposable-guest run was September 28. Its log is
`/home/ansible/bootstrap-gitops-test/logs/run-20260928T131652/bootstrap.log`.
Read-only inspection on October 4 established:

- The Terraform output fix worked in this run. GitLab seeding completed.
- GitLab state handoff was verified for seven components: GitLab, DNS, Vault,
  Authentik, Vault OIDC, Vault GitLab JWT, and MAAS.
- MAAS completed at 14:09:03 UTC. k3s then started and failed at 14:26:51 UTC
  downloading its verified binary: `The read operation timed out`.
- The k3s binary and final GitOps runtime marker are absent. No bootstrap,
  Terraform or Ansible process is currently progressing the run.
- No complete GitLab merge-to-service journey has passed.

A delegated probe on October 4 received HTTP 200 for both the pinned k3s
binary URL and checksum manifest from the guest. That supports a transient
or slow transfer diagnosis; HEAD requests do not prove a complete download.
Earlier source, generated-project and gateway component passes remain useful,
but they do not substitute for the missing combined test.

## Actual blockers and completion order

| Order | Work | Required result | Owner |
| --- | --- | --- | --- |
| 1 | Add bounded, checksum-preserving resilience to the k3s download; resume the failed stage and remaining bootstrap stages. | Installed k3s reports healthy; final GitOps runtime and credentials are configured; state handoff remains intact. | Cheaper implementation agent; primary reviews result. |
| 2 | Repair the acceptance fixture before trusting its verdict. | MR success belongs to the current commit; checkpoint progress is bound to that commit; resume can recover a failed main pipeline without blindly repeating an old failure. Focused regression checks cover these cases. | Cheaper implementation agent; primary reviews mutation and recovery behavior. |
| 3 | Run the actual GitLab workflow in the disposable guest. | A current-commit MR plan, merge and protected-main apply produce working DNS, trusted TLS, Helm deployment and host configuration. A core DNS/host-policy change also succeeds with the laptop controller absent from the apply path. | One execution agent; primary diagnoses only new failures. |
| 4 | Prove access and lifecycle behavior. | Actual installed Authentik allows the intended group and denies another; unchanged rerun preserves state/secrets; interruption resumes safely; reviewed deletion removes the service and reruns cleanly. | Same execution agent; primary verifies evidence. |
| 5 | Close the release. | Final source checks and one clean full run of the corrected workflow pass; durable sanitized evidence and exact first-use instructions are linked from the HTML report. | Primary review and concise documentation. |

Steps 1 and 2 can proceed independently. Steps 3 and 4 depend on both.
The existing fresh guest can establish integration progress after repair;
one final clean run establishes reproducibility after the code is stable.
Do not rebuild the entire guest for each localized failure.

The two confirmed fixture defects are in
`scripts/test-gitops-guest-merge.py`: `ensure_mr_plan` accepts historical green
pipelines without binding them to the current MR SHA, and `merge_and_apply`
waits on the same failed main pipeline after resume. Existing access tests use
a synthetic OIDC provider; an Authentik redirect alone does not prove actual
installed group enforcement. These gaps prevent a trustworthy completion
claim even after the k3s download succeeds.

## Execution limits that prevent another open-ended cycle

- Preserve the current guest and record a sanitized checkpoint after each
  successful milestone: source revision, stage, pipeline ID/SHA and result.
- Use explicit timeouts and finite retries. If a failure repeats after two
  focused attempts, diagnose or change the approach before another attempt.
- Run targeted checks for each repair, then the required final source gate.
  Do not repeatedly pay for full setup and broad checks without new evidence.
- Treat expired synthetic fixture credentials as fixture maintenance. Never
  import production secrets to make acceptance pass.
- Do not promise a completion time while the first real pipeline is still
  untested. Report the failing gate and concrete error, not an invented
  percentage complete or another general “still working” update.

## Scope and cost control

The first usable release targets a clean supported Ubuntu host and one
authenticated web application managed by GitLab. Node expansion, native
protocol/bare-metal adapters, multiple clusters and automated Kuma integration
are not gates for this work. Existing-host adoption and production disaster
recovery are not proven by this release. Accepted backup/recovery requirements
remain tracked; this plan does not silently mark them complete.

Two bounded investigations were actually delegated to `gpt-6-luna` on October
4: the download failure and remaining release gaps. Use that cheaper model for
small repairs and evidence collection, with the primary agent handling scope,
security-sensitive review and difficult cross-component failures. Keep one
agent responsible for VM mutation. Parallel agents are useful only when tasks
are independent; duplicated reviews and idle orchestration add cost. The
primary conversation model has not been switched. Official documentation
supports per-agent model selection:
[OpenAI subagents documentation](https://learn.chatgpt.com/docs/agent-configuration/subagents).

The reusable execution permission and its activation limits are recorded in
[the permission audit](../docs/development-permissions.md). It covers trusted
repository checks and disposable fixtures, not real infrastructure or
publishing. No additional product decision is needed to execute this plan.
