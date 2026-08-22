# SSH Host Trust and Key-Change Recovery

> Status: Accepted target state
> Last updated: 2026-07-26

## Goal

The bootstrap must protect the administrator from connecting to the wrong
machine without forcing a new homelab operator to manage `known_hosts` manually.
The same pinned identity must be used by the Go SSH client and Ansible.

## Trust Store

Bootstrap maintains a dedicated OpenSSH known-hosts file on the controller:

```text
~/.local/share/bootstrap-home-infra/ssh/known_hosts
```

The directory is mode `0700`; the file is mode `0600`. Writes are atomic. Go and
Ansible both use this exact file. Repository-wide settings must not disable host
key verification or point production runs at test credentials.

The optional structural config field `ssh_host_fingerprint` contains an expected
SHA-256 fingerprint and is safe to store in `config.yaml`.

## Interactive First Connection

When no pin exists:

1. Connect only far enough to receive the server host key.
2. Display the target hostname/IP, resolved address, key algorithm, and SHA-256
   fingerprint in a verification panel.
3. If `ssh_host_fingerprint` is configured, require an exact match.
4. Otherwise ask the operator to accept the observed fingerprint.
5. On acceptance, atomically write the host key to the bootstrap trust store.
6. Reconnect using strict verification before sending credentials or running a
   command.

Declining stops without writing a pin.

## Interactive Changed-Key Recovery

A mismatch is a high-risk event, not an ordinary yes/no retry:

1. Stop before authentication and display a red warning explaining that the
   server may have been reinstalled or may be impersonated.
2. Display the target, resolved address, key algorithm, previously pinned
   fingerprint, and newly observed fingerprint.
3. Ask whether the operator intentionally reinstalled or replaced this server.
4. Require the operator to type the target hostname/IP to confirm replacement.
5. Atomically replace only that target's entry.
6. Reconnect and verify that the server presents the same new key before
   authentication.
7. Log the target, old fingerprint, new fingerprint, timestamp, and interactive
   approval result. Host public-key fingerprints are not secrets.

Cancellation or a second mismatch leaves the old pin intact and stops the run.
No external `ssh-keygen -R` step is required.

## Headless Behavior

Headless mode never prompts and never silently replaces a changed key.

- An existing matching pin proceeds.
- A configured `ssh_host_fingerprint` can establish the first pin when the
  observed key matches.
- `--accept-new-host-key` may establish a first pin only when no entry exists.
  It cannot replace an existing mismatched pin.
- Replacing a pin requires both `--replace-host-key` and
  `ssh_host_fingerprint` set to the expected new fingerprint.
- A mismatch without both requirements is a hard failure before authentication.

Tests use an isolated temporary known-hosts file and explicit fingerprints; they
never modify the operator's trust store.

## Hostname and Address Rules

Pins are keyed using OpenSSH host syntax, including `[host]:port` for non-default
ports. Bootstrap records the operator's configured target and the resolved
address presented during confirmation. A DNS change that leads to a different
host key follows the changed-key recovery flow.

## Ansible Contract

The Go runner generates inventory with the runtime SSH user and key path and
invokes Ansible with:

```text
ANSIBLE_HOST_KEY_CHECKING=True
ansible_ssh_common_args=-o UserKnownHostsFile=<bootstrap-known-hosts>
```

Ansible must never use `host_key_checking = False` in production. Test
configuration may use a separate, explicitly scoped trust file but follows the
same verification behavior.

## Required Implementation Changes

- Replace `ssh.InsecureIgnoreHostKey()` with a strict known-hosts callback.
- Add first-use and changed-key TUI messages and confirmation flow.
- Add `ssh_host_fingerprint`, `--accept-new-host-key`, and
  `--replace-host-key`.
- Make Go and Ansible share the same runtime trust store.
- Remove production `host_key_checking = False` defaults.
- Add unit tests for new, matching, changed, rejected, atomically replaced, and
  second-mismatch keys.
- Add integration coverage proving no SSH command runs before trust succeeds.
