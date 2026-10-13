# Development permission audit — updated 2026-10-04

## October 4 correction and installed scope

The earlier audit created the repository rule but did not install a matching
copy in the user rules directory. On October 4, the reviewed rule was copied
to `/Users/brian/.codex/rules/bootstrap-home-infra.rules` with execution approval.
The installed copy matches `.codex/rules/development.rules` byte for byte.
`codex execpolicy check` confirms the helper prefix is allowed and this rule
does not match arbitrary Python execution.

The current session already advertised this helper prefix as approved, so the
missing user-level copy does not explain every prompt. Hermit cache writes
outside the sandbox can still require an escalated execution request. Use the
exact absolute helper invocation below, with the execution tool's escalation
setting when needed, rather than inventing new shell wrappers or per-command
permissions. A rule permits execution outside the sandbox; it does not make an
ordinary sandboxed invocation able to write protected caches.

This single scope covers the remaining release work:

- Source checks, builds, declared dependency downloads and managed tool caches.
- Creation, start, stop and replacement of repository-owned disposable QEMU
  guests and mock containers through the enumerated tasks.
- Source transfers, diagnostics, logs, targeted bootstrap stages, test-only
  credentials and GitLab merge/pipeline tests inside the fixed disposable guest.

Repository file edits and report updates remain covered by the working
agreement. Real infrastructure, production credentials, publishing and remote
repository changes remain outside this permission. This is permission to run
trusted repository code, not technical isolation against malicious code or
guest commands that contact other systems.

Codex loads rules at startup; restart the Codex session to ensure the installed
user rule is loaded. Managed application policy can still impose approvals.
No global approval or sandbox setting was weakened. See the
[official OpenAI rules documentation](https://learn.chatgpt.com/docs/agent-configuration/rules).

## Original September 27 audit

The user requested fewer repeated approvals with a proper scope. The shared
Codex rule file contained 180 entries, including individual source transfers and
test commands with different log filenames. Shell redirection can cause Codex
to evaluate the entire shell string instead of reusing a command-prefix rule.

## Scoped execution

The project-local `.codex/rules/development.rules` permits the absolute Hermit
Python path followed by this checkout's `scripts/dev-authorized.py`. The helper
fixes its working directory to this repository and accepts:

| Operation | Scope |
| --- | --- |
| `task NAME [--log]` | Enumerated source checks, declared dependency setup, build, GitOps tests, disposable QEMU lifecycle, and mocks. One task, no extra task arguments. |
| `guest COMMAND [--log]` | Commands inside `ansible@127.0.0.1:2222` with the repo's test key. Host verification enabled, SSH config and forwarding disabled, guest hostname/user checked. |
| `put SOURCE` | Allowed repository source file to the same relative path in the guest checkout. Rejects traversal, local symlinks, dotfiles, keys, tfvars, state, and installed collections. |

The task list also includes `release-status`, `release-source`,
`release-standup`, `release-verify`, and `test-release-check`. These use the same
fixed guest and declared-source boundary. The standup runner additionally
transfers Hermit links/pins and the fixed disposable test SSH key needed by the
guest-local controller; it never selects arbitrary user keys or a target host.
See [the release-check contract](experimental-release-check.md).

Logs use private unique files under `/tmp`, avoiding shell redirection. Use the
absolute invocation when requesting the saved permission:

```sh
/Users/brian/src/bootstrap-home-infra/bin/python /Users/brian/src/bootstrap-home-infra/scripts/dev-authorized.py task lint-all --log
/Users/brian/src/bootstrap-home-infra/bin/python /Users/brian/src/bootstrap-home-infra/scripts/dev-authorized.py put internal/gitops/project.go
/Users/brian/src/bootstrap-home-infra/bin/python /Users/brian/src/bootstrap-home-infra/scripts/dev-authorized.py guest 'hostname'
```

This approves trusted repository code outside the sandbox, not arbitrary local
Python or shell execution. It is not a sandbox against malicious changes to
source, recipes, or commands inside the guest. Guest work must remain confined
to disposable fixtures; production credentials and real-system mutations are
not authorized. Review the helper's scope when changing it.

## Existing permissions and activation

Shared rules already include broader permissions for `bin/python`, `bin/just`,
`ssh -p 2222`, `git push`, and `gh pr`. They were left unchanged because other
projects share them. The new rule does not revoke those permissions or replace
the real-system and publishing boundaries in `AGENTS.md`. Global sandbox,
network, approval, and filesystem settings remain unchanged.

Restart Codex before relying on the user-level rule: rules load at startup and
the current conversation may retain its previous policy. Validation uses
`codex execpolicy check` plus focused helper boundary tests. This audit does not
mutate live infrastructure.

Reference: [OpenAI command rules](https://developers.openai.com/codex/rules).
