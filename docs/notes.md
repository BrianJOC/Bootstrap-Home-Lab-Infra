# Implementation notes

Updated: 2026-10-14. Reviewed against commit `ec5d743` and the current source.

## Latest bootstrap fixes

- Removed unresolved merge markers in configuration validation while retaining
  both internal-domain and data-disk validation.
- Updated storage setup to the stage interface that passes configuration,
  secrets, and run context separately. Its CLI name is `storage-setup`; it
  returns without changes when `data_disk` is unset.
- Wired the TUI runner to the current stage API and propagated runner errors
  back to the command after the TUI exits.
- Made wizard results pass shared configuration validation, including required
  secrets and optional numeric `lab_vlan_id` parsing.
- Added `lab_vlan_id` to generated YAML and defaulted the Vault init output
  path to `./vault-init.json` when loading config or completing the wizard.

## Current configuration flow

- Missing or incomplete configuration opens the wizard in interactive mode.
  Valid configuration opens the preamble; `--wizard` forces the wizard.
  Malformed YAML fails during loading before a screen is selected.
- Completing the wizard writes structural configuration and secrets to the
  paths selected by `--config` and `--env`. Each file is written with mode
  `0600` using a temporary file and rename; the pair is not one transaction.
- Accepting existing values at the preamble skips wizard file writes. Stage 0
  still validates configuration, checks SSH and the target OS/architecture,
  rewrites structural YAML, and writes the run manifest. It does not write `.env`.
- Headless mode loads YAML plus secrets from the environment or `.env` and
  requires complete configuration. Selecting `config-collection` defers the
  validation to Stage 0; it does not provide a headless setup wizard.
- `--stage` runs only the named stage; it does not automatically run its
  prerequisites. The registered sequence is config collection, optional storage,
  DNS, TLS, Vault, Authentik, PostgreSQL, GitLab, MAAS, and k3s.

## Follow-up and verification

- The wizard constructs a fresh configuration from its fields. Optional storage
  settings and custom Vault settings are not carried through that mapping;
  review those values after using `--wizard` on an existing configuration.
- The current Justfile contains test infrastructure helpers, but no build,
  lint, VM lifecycle, or Terraform migration/import recipes. Use the direct
  commands in the README; older design documents may name planned recipes.
- `workflow.md`, `specs.md`, and the documents under `superpowers/` describe
  design intent. Their broader recovery, GitOps, and migration flows should
  not be treated as evidence that integration validation has passed.
- This documentation update was checked against source and task definitions.
  It did not deploy services or run QEMU, Molecule, Terraform, or end-to-end
  bootstrap validation.
