# Controller Laptop Recovery

> Status: Accepted target state — 2026-07-28
> Applies to: loss, replacement, or compromise of the single administrator laptop

## Decision

The platform can be adopted from a blank replacement laptop without access to
the old laptop or its filesystem.

The minimum recovery kit is:

1. the operator's password manager;
2. a verified TrueNAS or USB Restic repository;
3. network or physical-console access to the target server; and
4. a verified bootstrap release, or the release/source artifact preserved in
   the recovery set.

The controller is not a second source of truth. GitLab, Vault, the target
runtime, and verified backups remain authoritative for their owned state. The
controller recovery bundle is a portable map that lets a replacement laptop
find, authenticate to, inspect, and deliberately adopt those systems.

## Recovery bundle

When automated backups are enabled, bootstrap creates a versioned controller
recovery bundle:

- after initial bootstrap;
- after accepted structural configuration, ownership, backend, SSH identity,
  or controller-credential changes;
- after a successful infrastructure apply, as part of the critical recovery
  checkpoint; and
- nightly, by including the most recently sealed bundle in the ordinary backup
  set.

The controller creates or refreshes the bundle. The target may retain and back
up only the resulting opaque encrypted artifact; it does not receive the
controller-only encryption secret.

Every bundle has a schema version, creation time, controller ID, target ID,
source commit, bootstrap version, and content hashes. Recovery refuses an
unsupported schema, a failed hash, a mismatched target identity, or an
unverified release.

### Included by default

- sanitized structural configuration, with paths expressed portably rather
  than copied as laptop-specific absolute paths;
- target address and the accepted SSH host-key algorithm and SHA-256
  fingerprint;
- controller and target identity markers;
- GitLab URL, project IDs, critical repository names, backend/workspace
  identifiers, and last verified commit, lineage, and serial metadata;
- Restic repository identity and location, but not its password;
- service endpoints, certificate metadata, and non-secret ownership manifests;
- run manifests, accepted policy values, and last-known protection status;
- the exact bootstrap release artifact or source reference, platform, checksum,
  and build/source commit;
- a portable recovery README and the verification commands required before
  adoption; and
- the current controller SSH private key, inside an additional encrypted
  envelope.

The bundle itself is protected by the Restic repository encryption. The SSH
private key is encrypted again with a distinct controller-recovery secret kept
only in the password manager. A compromise of the target and its runtime Restic
password therefore does not by itself disclose the controller SSH key.

### Excluded by default

- `vault-init.json`, Shamir unseal shares, and the Vault initial root token;
- Restic repository passwords;
- the controller-recovery encryption secret;
- Cloudflare, paging, GitLab, Vault, or other service credentials that can be
  entered from the password manager;
- unencrypted `.env` content;
- decrypted Terraform state outside the separately protected GitLab recovery
  checkpoint; and
- caches, downloaded packages, and other replaceable laptop state.

Bootstrap never discovers or ingests `vault-init.json` merely because it is
near the configured output path. The recovery manifest names Vault recovery
material as a prerequisite without copying it.

## Controller identity and adoption

This is a single-controller operating model. Initial bootstrap creates a random
controller ID and records an adoption marker on the target containing:

- controller ID;
- target ID;
- adoption time;
- last successful contact time;
- last bundle ID; and
- last successful apply or recovery checkpoint.

The marker is not a distributed lock and does not make the target dependent on
the laptop. A replacement controller displays the marker and warns when another
controller was recently active. It performs no mutation until the operator
explicitly adopts the target.

Adoption writes a new controller ID and an auditable event. It does not
automatically revoke the previous controller because an unavailable laptop is
not necessarily lost or compromised.

## Recovery modes

### Old controller unavailable

This is the default mode. It preserves valid controller credentials while
reconstructing access on the replacement laptop.

- Restore the encrypted SSH private key when available.
- Preserve the target's authorized key and controller-specific service tokens.
- Warn that the old controller may still be capable of access.
- Require explicit adoption before any change.

This mode is appropriate for hardware failure, replacement, or temporary lack
of access when compromise is not suspected.

### Old controller lost or compromised

The operator must explicitly select this mode. After the replacement controller
has proved access and completed its read-only audit, bootstrap:

1. generates or selects a new controller SSH key;
2. installs and verifies the new public key before removing the old one;
3. rotates controller-specific GitLab, Vault, Cloudflare, paging, backup, and
   other tokens that were present on the old laptop;
4. invalidates old controller sessions where the service supports it;
5. regenerates `.env` and protected controller state with the new credentials;
6. produces and verifies a new recovery bundle; and
7. records which credentials were rotated, deferred, or require manual action.

Vault Shamir shares are not automatically rotated. If their exposure is
suspected, the tool presents the separate Vault rekey procedure and requires
the threshold of existing shares.

No old credential is removed until its replacement passes an authentication
test. A partial rotation leaves a visible incident checklist and does not claim
the compromised controller has been revoked.

### Original SSH private key unavailable

When the bundle or password-manager copy of the SSH key cannot be opened,
recovery supports a new-key path:

1. generate a new key locally;
2. display its public-key fingerprint;
3. require physical console or another already trusted administrative channel
   to add that public key to the target;
4. prove key-based login with strict host verification; and
5. continue in the lost-or-compromised mode.

The tool never weakens SSH host checking or enables password authentication to
work around a missing key.

## SSH server identity

For an existing target, the saved host fingerprint is expected identity, not a
new trust-on-first-use observation. Recovery compares the live key before
authentication and stops on a mismatch.

If the operator intentionally rebuilt or replaced the target, recovery uses the
accepted changed-key workflow: show old and new fingerprints, require
target-specific confirmation, replace the pin atomically, and reconnect
strictly. Headless recovery requires the expected new fingerprint explicitly.
See [SSH host trust](ssh-host-trust.md).

## Guided recovery flow

`bootstrap controller recover` follows this order:

1. **Acquire the tool.** Download a release or restore the preserved artifact,
   then verify its checksum and source/build identity.
2. **Open the repository.** Retrieve the repository password from the password
   manager and validate the expected Restic repository ID.
3. **Select a bundle.** Show creation time, target ID, controller ID, source
   commit, integrity result, and protection age before restore.
4. **Open protected material.** Prompt, masked, for the controller-recovery
   secret and decrypt the SSH key only into a mode `0600` destination selected
   for the replacement laptop.
5. **Rebind local paths.** Reconstruct `config.yaml`, `.env`, the bootstrap-owned
   known-hosts file, and `~/.bootstrap` atomically using current laptop paths.
   Missing service secrets are requested from the password manager rather than
   silently omitted.
6. **Verify identity.** Compare the target ID and pinned SSH fingerprint before
   authentication.
7. **Audit read-only.** Inspect target health, Vault seal state, GitLab,
   Terraform backends, applied commits/state lineage and serial, backups, DNS,
   certificates, ownership markers, and active freeze/maintenance state.
8. **Review differences.** Display stale bundle data, target drift, recently
   active controller markers, missing credentials, and every proposed local
   write or remote rotation.
9. **Adopt explicitly.** Require the target name and chosen recovery mode before
   writing the new controller marker or making a remote change.
10. **Verify and checkpoint.** Run a non-mutating platform plan and service
    checks, create a new sealed bundle, restore-open it, and record the recovery
    evidence.

Recovery must not treat successful SSH login as proof that the platform is
safe to mutate.

## Trial mode

With `backup_enabled: false`, controller reconstruction is not automatically
protected. The wizard offers:

```text
bootstrap controller export --output <external-path>
```

This creates the same encrypted portable bundle and verifies it after writing.
The destination should be removable storage or another device; a bundle kept
only on the laptop does not protect against laptop loss. Status remains
`Controller recovery: not durably protected (trial mode)` until a verified
external bundle or Restic snapshot exists.

## Failure behavior

| Failure | Required behavior |
|---|---|
| Restic repository ID differs | Stop before restore; never guess another repository |
| Bundle hash or schema fails | Mark unusable and offer older verified bundles |
| Target ID differs | Stop adoption and require target-selection review |
| SSH fingerprint differs | Use the explicit changed-key workflow; never bypass checking |
| SSH key envelope cannot open | Preserve files, disclose no key material, and offer console enrollment |
| Required password-manager item is absent | Keep the audit incomplete and name the missing capability |
| Another controller was recently active | Warn and require explicit adoption; do not auto-revoke |
| Credential rotation partly fails | Preserve working replacement access and report every unrevoked old credential |
| Post-recovery checkpoint fails | Keep the controller adopted but mark recovery protection degraded |

## Required implementation evidence

- A blank VM or laptop recovers control using only the documented minimum kit.
- Recovery works when the old laptop and all of its absolute paths are absent.
- Bundle creation, nightly inclusion, post-change refresh, and post-apply
  checkpoint inclusion are tested.
- Restic encryption alone cannot open the separately encrypted SSH key.
- `vault-init.json` and unseal shares never appear in the bundle.
- Existing-target recovery rejects a changed SSH fingerprint before
  authentication.
- Normal adoption preserves old credentials and records the warning.
- Compromised-device adoption rotates the old SSH key and inventories all
  controller-specific token rotations.
- New-key console enrollment works without enabling password SSH or disabling
  host verification.
- Recovery writes `.env`, SSH keys, and protected controller state with mode
  `0600` and never logs their contents.
- The read-only audit is demonstrably mutation-free.
- A newly created bundle is restored and verified before recovery is declared
  complete.
