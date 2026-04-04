# First-Run Setup Wizard — Design Spec

**Date:** 2026-04-03
**Branch:** feat/rpi-dual-storage (base for implementation)
**Status:** Approved for implementation

---

## Goal

Replace the broken first-run experience — where launching the binary without a `config.yaml`
immediately errors — with a guided sequential setup wizard that collects all required
configuration, writes `config.yaml` and `.env`, and transitions seamlessly into the stage
runner dashboard.

---

## Problem Statement

The current flow requires the user to hand-craft a `config.yaml` and `.env` before the TUI
will start. There is no guidance on what fields are required, what format they take, or where
to find values like a Cloudflare Zone ID. This is a broken first-run experience even for
technical users.

---

## Architecture

### Screen Router Pattern

One `tea.Program`. One top-level `AppModel` that holds an active `Screen` interface and
delegates `Init`, `Update`, and `View` to it. Screen transitions happen by swapping the
active screen — no `mode` string, no god model.

```
internal/tui/
  app.go       — AppModel (router): delegates to active Screen, handles WindowSizeMsg
  wizard.go    — WizardModel: sequential field collection, self-contained
  model.go     — ProgressModel: existing stage runner view (renamed from Model)
  styles.go    — shared lipgloss styles used by both screens
  sink.go      — unchanged
```

```go
type Screen interface {
    Init() tea.Cmd
    Update(tea.Msg) (Screen, tea.Cmd)
    View() string
}

type AppModel struct {
    current Screen
    width   int
    height  int
}
```

### Visual Consistency

Both screens import from `styles.go` — same header style, same color palette, same border
primitives. The wizard looks like it belongs to the same tool because it uses the same styles,
not because it shares a model.

### No New Dependencies

`textinput.Model` from `charmbracelet/bubbles` (already a dependency) handles text input.
Its `EchoModePassword` handles masked secret fields. All layout is lipgloss.

---

## Entry Paths

### 1. No `config.yaml` (first run)
```
binary launched → config.Load() returns incomplete RunConfig
→ config.Validate() fails
→ AppModel starts with WizardModel (empty/partial pre-fill)
→ wizard collects fields → writes config.yaml + .env
→ transitions to ProgressModel → stage runner starts
```

### 2. `config.yaml` present, no `--wizard` flag
```
binary launched → config.Load() returns complete RunConfig
→ config.Validate() passes
→ WizardModel preamble: "Found config.yaml — use it? [Y/n]"
→ Y: skip wizard, AppModel starts with ProgressModel directly
→ n: AppModel starts with WizardModel, all fields pre-filled
```

### 3. `--wizard` flag
```
binary launched → config.Load() (file may or may not exist)
→ AppModel always starts with WizardModel
→ all fields pre-filled from loaded config where available
→ user steps through fields, editing as needed
→ writes updated config.yaml + .env → transitions to ProgressModel
```

---

## Wizard Fields

Nine fields in order. Structural fields first, secrets last.

| # | Config key | Label | Smart default | Type |
|---|-----------|-------|---------------|------|
| 1 | `target_host` | Target host | — | text; e.g. `192.168.1.100` |
| 2 | `ssh_user` | SSH user | `ubuntu` | text |
| 3 | `ssh_key_path` | SSH private key | Scan `~/.ssh/id_ed25519`, `~/.ssh/id_rsa` | text + detect flow |
| 4 | `internal_domain` | Internal domain | — | text; e.g. `home.lab` |
| 5 | `cloudflare_zone_id` | Cloudflare Zone ID | — | text |
| 6 | `bootstrap_server_ip` | Bootstrap server IP | Derived from `target_host` if bare IPv4 | conditional |
| 7 | `lab_vlan_subnet` | Lab VLAN subnet | — | text; e.g. `10.0.0.0/24` |
| 8 | `admin_email` | Admin email | — | text |
| 9 | `CF_DNS_API_TOKEN` | Cloudflare API token | — | **masked** |
| 10 | `BOOTSTRAP_ADMIN_PASSWORD` | Admin password | — | **masked + confirm** |

**`target_host` vs `bootstrap_server_ip`:** These represent the same server but different
concerns. `target_host` is the SSH endpoint (can be hostname, may include port).
`bootstrap_server_ip` is the bare IPv4 used inside service config files on the server.
The wizard derives `bootstrap_server_ip` automatically:
- If `target_host` is a bare IPv4 → use it directly, no separate prompt
- If `target_host` is a hostname or includes a port → strip port, attempt DNS resolution,
  present resolved address as default, prompt to confirm or override

**Optional fields** (`lab_vlan_id`, `vault_shares`, `vault_threshold`) have hardcoded
defaults (0, 5, 3) and are not shown as wizard steps. An "Advanced" note on the VLAN subnet
field (`press A to configure advanced options`) provides access without cluttering the flow.

**SSH key detection flow:**
```
Check ~/.ssh/id_ed25519, ~/.ssh/id_rsa, ~/.ssh/id_ed25519_sk
  → Found: "Detected: /Users/brian/.ssh/id_ed25519  [↵ use · type to override · g generate]"
  → Not found: "No key detected  [type a path · g generate]"
  → g: generate new ed25519 key, prompt for save path, show public key fingerprint
```

---

## Per-Field Screen Layout

```
 bootstrap-home-infra ──────────────────────────────────────────

  SSH Private Key                                        [3 of 9]
  Path to the private key used to reach your server.
  Example: ~/.ssh/id_ed25519

  Detected: /Users/brian/.ssh/id_ed25519

  > /Users/brian/.ssh/id_ed25519_

  ↵ accept · g generate · esc go back

```

Components (top to bottom):
1. Shared header (`styleHeader`) — identical to progress screen
2. Field label + `[N of 9]` counter
3. One-line description
4. Example value (dimmed)
5. Detected default (if any, highlighted)
6. Text input (focused, cursor visible)
7. Inline validation error (red, below input, only when present)
8. Available actions (dimmed hint line)

---

## Navigation

| Key | Action |
|-----|--------|
| `Enter` | Accept value, validate inline, advance to next field |
| `Esc` | Go back to previous field (current value preserved) |
| `Ctrl+C` | Cancel wizard entirely — no files written, binary exits |
| `g` | (SSH key field only) Generate new keypair |
| `A` | (VLAN subnet field only) Open advanced options sub-flow |

Inline validation fires on `Enter`. If invalid, error appears below the input and the field
is re-presented. The user never advances past a field with a bad value.

---

## Preamble Screen (existing config)

Shown before the wizard fields when `config.yaml` is present and complete:

```
 bootstrap-home-infra ──────────────────────────────────────────

  Found config.yaml

  target_host:    192.168.1.100
  ssh_user:       ubuntu
  internal_domain: home.lab
  admin_email:    admin@example.com
  ...

  Use this config and proceed?  [Y/n]
```

`Y` (or `Enter`) → `AppModel` transitions directly to `ProgressModel`, stage runner starts.
`n` → `WizardModel` opens with all fields pre-filled from the loaded config.

---

## Data Flow

```
WizardModel collects strings only.
Emits: WizardResult{Values map[string]string, Secrets map[string]string}

main.go receives WizardResult:
  1. Translate Values + Secrets → config.RunConfig
  2. config.Validate(rc) — return error to wizard if invalid (shouldn't happen; wizard validates inline)
  3. writeConfigYAML(rc.Config)
  4. writeEnvFile(rc.Secrets)
  5. Signal AppModel to transition to ProgressModel
  6. Start stage runner goroutine
```

`WizardModel` never imports the `config` package. It operates on plain strings.
The `tui` package dependency graph is unchanged.

---

## ConfigStage Changes

`ConfigStage` (stage 0, `"config-collection"`) loses field collection.
Remaining responsibilities:
- Validate the already-complete `RunConfig` (quick sanity check)
- Test SSH connectivity
- Detect target arch and OS
- Run disk picker (interactive, via existing `sink.Prompt`/`sink.Verify`)
- Call `writeConfigYAML` once more after disk picker (to persist `data_disk`/`data_disk_mode`)
- Write run manifest

`writeEnvFile` moves out of `ConfigStage` entirely — it is called in `main.go` at wizard
completion. `ConfigStage` never touches secrets.

---

## `--wizard` Flag

```go
rootCmd.Flags().BoolVar(&flagWizard, "wizard", false,
    "Run setup wizard (pre-fills from existing config if present)")
```

When set, `AppModel` always starts with `WizardModel` regardless of whether `config.yaml`
exists or is complete.

---

## Error Handling

| Scenario | Behaviour |
|----------|-----------|
| User cancels with `Ctrl+C` | No files written, binary exits cleanly |
| Inline validation failure | Error shown below input, field re-presented |
| SSH key generation failure | Error shown inline, user prompted to enter path manually |
| DNS resolution failure for `target_host` | Warn inline, prompt user to enter IP manually |
| `writeConfigYAML` or `writeEnvFile` fails | Fatal error shown in wizard screen, binary exits |

---

## Testing

| Layer | What to test |
|-------|-------------|
| `WizardModel` unit tests | Field advancement, back navigation, validation errors, masking, smart defaults |
| `AppModel` unit tests | Screen transitions: wizard→progress, preamble→progress, preamble→wizard |
| `main.go` integration | Entry path selection (no config / complete config / --wizard flag) |
| SSH key detection | Standard path detection, missing path, generate flow |
| `bootstrap_server_ip` derivation | Bare IP, hostname with port, unresolvable hostname |
