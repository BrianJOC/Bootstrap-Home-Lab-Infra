# Repeatable experimental release check

This is the short feedback loop for the no-backup, GitLab-managed web-service
release. It targets **only the repository's disposable ARM64 Ubuntu QEMU guest**.
It does not deploy to a real Raspberry Pi or change your home network.

> October 13 review: source implementation has advanced beyond the initial
> runner described below. Installed-Authentik, interrupted-reconcile and
> seed-source scripts now exist, and the runner declares their gates plus a
> fresh-install gate. Its fixture tests and evidence wiring remain incomplete;
> `verify` omits the merge script's required source fingerprint, and the new
> evidence gates are not recorded. Treat this document's missing-test table as
> the original contract, not a current inventory. Follow the
> [restart checklist](../reports/restart-review-2026-10-13.md) before using it
> to claim completion. Experimental QEMU `ready` and physical `physical_ready`
> are now separate verdicts; no current combined pass exists.

## Commands

Use the existing Hermit environment from the repository root:

```sh
bin/just release-status     # quick current guest probe + overall YES/NO
bin/just release-source     # source, fixture, generated-project and gateway tests
bin/just release-standup    # start mocks, create/resume test VM, sync source, bootstrap
bin/just release-verify     # real guest GitLab deploy/rerun/removal acceptance
```

Run source → standup → verify → status for the complete available sequence.
Fix the first failing gate and repeat its command. `release-status` is read-only
on the guest; it writes local evidence. The standup and verification commands
mutate only disposable infrastructure with synthetic credentials.

For Codex, all four are available through the previously approved entry point:

```sh
/Users/brian/src/bootstrap-home-infra/bin/python /Users/brian/src/bootstrap-home-infra/scripts/dev-authorized.py task release-status
```

Replace the final task name for the other commands. No new general shell or
network permission is needed; managed application policy still applies.

## Results and exit codes

Every completed run writes `logs/release-check/result.json` containing `ready`,
`command_passed`, the source-content fingerprint and individual check results.
`status` exits **0 only for overall YES**, otherwise 1. The other commands exit
0 when their own sequence passed, 1 when it failed. An internal runner error
exits 2. A passing `source` command is not a deployment-ready verdict.

Results use `PASS`, `FAIL`, `NOT_RUN`, `RUNNING`, `STALE`, and `MISSING_TEST`. Failed
commands point to separate mode-0600 logs under the ignored
`logs/release-check/` directory. The short output shows only fixed diagnostic
categories and stage names; detailed command logs stay local and must be
reviewed/redacted before sharing. No raw log is committed as release evidence.

Source changes invalidate earlier recorded passes. Guest replacement or reboot
invalidates bootstrap/lifecycle evidence. `status` probes runtime again; saved
component checks display their prior timestamps in JSON. An unfinished
acceptance lifecycle resumes its saved MR/pipeline progress; invoking `verify`
after a successful lifecycle starts a new one. Missing coverage never counts as
a pass, and there is no manual “mark passed” option.

## What is tested and what must still be implemented

| Gate | Check and pass condition | Current automation |
| --- | --- | --- |
| Source | Go tests/build/vet, formatting/conflicts, Ansible syntax/lint, Terraform validation | Executable |
| Fixture reliability | Wrong-SHA plans rejected; main pipeline recovery; stale checkpoint invalidation; verifier cannot turn missing coverage green | Executable unit tests |
| Generated project | Generated source/toolchain/chart validation; real Terraform fresh/no-change/interrupted state lifecycle | Executable component tests |
| Gateway | Real OAuth2 Proxy admits allowed synthetic OIDC group and denies another | Executable component test |
| Runtime | ARM64 Ubuntu, backups and controller recovery disabled, k3s API ready, GitOps marker/helper present | Executable quick probe |
| Standup | Bootstrap finishes successfully using the fixed disposable configuration | Executable, bounded to three hours |
| GitLab lifecycle | Current-SHA MR plan → merge → successful main reconcile → DNS/TLS/app/core-host checks → no-change → reviewed removal → no-change | Executable, bounded to four hours total |
| Installed identity | Actual Authentik users in allowed/denied groups log in through the deployed gateway; expected allow/deny responses | **Missing test**; mock OIDC and redirects do not satisfy this |
| Full interruption | Interrupt actual bootstrap/CI at defined checkpoints, rerun, verify stable credentials/state and eventual convergence | **Missing test**; Terraform-only interruption does not satisfy this |
| Clean current source | Fresh guest finishes the whole journey; seeded GitLab code is compared to the intended current source | **Missing test**; copying scripts to an old guest does not update or validate its seeded GitLab code |
| Raspberry Pi | Supported 64-bit Ubuntu boots on the actual Pi; kernel/cgroups/storage/memory and the complete CI workload pass | **Missing hardware evidence**; QEMU ARM64 cannot certify this |

The four missing gates are explicit entries in `scripts/release-check.py`.
Replace each with a real executable assertion when implemented. This first
runner will intentionally continue to report **NO** even if every currently
implemented check passes. That exposes the remaining work instead of hiding it
behind a green partial test suite. Backups are deliberately excluded from this
experimental profile; this is not disaster-recovery certification.

## Standup behavior and prerequisites

The integration doctor requires Docker, QEMU, the existing base image and test
SSH key. If setup is missing, its log names the prerequisite and command; the
runner does not silently replace keys or rebuild the base image. SSH must trust
the fixed guest identity; changed keys fail closed under the accepted
[SSH trust contract](ssh-host-trust.md).

Standup starts mock ACME/Cloudflare services, resumes an existing snapshot when
the VM is stopped, or starts a fresh overlay when none exists. It never invokes
`vm-down` or deletes the current snapshot. It transfers declared source and only
the fixed test SSH key; real `.env`, controller state, Terraform state and user
keys are excluded. Extraction uses Python's safe tar data filter. Source sync
is an update, not a clean mirror, another reason clean-source proof is separate.

Bootstrap checks already-completed stages and runs missing stages. Dependency
setup is cached against its inputs rather than clearing the Python environment
on every retry. The k3s download retains checksum verification with bounded
timeouts/retries. Local and guest deadlines bound the commands; local and guest
locks reject concurrent bootstrap runs. GitLab jobs already submitted may
continue after the host verifier times out: inspect their saved IDs, then resume
instead of starting another bootstrap.

For a targeted diagnostic retry using the same fixture:

```sh
/Users/brian/src/bootstrap-home-infra/bin/python /Users/brian/src/bootstrap-home-infra/scripts/dev-authorized.py guest 'cd /home/ansible/bootstrap-gitops-test && bash scripts/test-gitops-guest-bootstrap.sh --stage k3s'
```

Finish with the full standup command so later stages also run. A targeted stage
pass alone is not completion evidence. Once the missing gates are executable,
the same loop can become the release gate without repeating a general audit.
