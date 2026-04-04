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

One `tea.Program`. One top-level `AppModel` that implements `tea.Model` and holds an active
internal `screen` interface, delegating `Init`, `Update`, and `View` to it. Screen transitions
happen by swapping the active screen — no `mode` string, no god model.

```
internal/tui/
  app.go       — AppModel (router): implements tea.Model, delegates to active screen
  wizard.go    — wizardModel: sequential field collection, self-contained
  model.go     — progressModel (renamed from Model): existing stage runner view
  styles.go    — shared lipgloss styles used by both screens
  sink.go      — unchanged
```

```go
// screen is an unexported interface within the tui package.
// AppModel implements tea.Model and holds a screen internally.
type screen interface {
    init() tea.Cmd
    update(tea.Msg) (screen, tea.Cmd)
    view() string
    setSize(width, height int)
}

// AppModel is the root tea.Model passed to tea.NewProgram.
type AppModel struct {
    current screen
    width   int
    height  int
}

// AppModel satisfies tea.Model:
func (a AppModel) Init() tea.Cmd                           { return a.current.init() }
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { /* see Screen Swap Convention */ }
func (a AppModel) View() string                            { return a.current.view() }
```

**Screen Swap Convention:** `update` returns `self` (the same screen value) when no transition
is needed. `AppModel.Update` compares the returned screen to `a.current` by interface identity.
When a different screen is returned, `AppModel`:
1. Replaces `a.current` with the new screen
2. Calls `newScreen.init()` to obtain any startup commands
3. Returns `tea.Batch(cmdFromUpdate, cmdFromInit)` so both are executed

`AppModel.Update` handles `tea.WindowSizeMsg` directly — calls `setSize` on the active screen
before delegating — so both screens receive correct terminal dimensions.

### Visual Consistency

Both screens import from `styles.go` — same header style, same color palette, same border
primitives. The wizard looks like it belongs to the same tool because it uses the same styles.

### No New Dependencies

`textinput.Model` from `charmbracelet/bubbles` (already a dependency) handles text input.
Its `EchoModePassword` handles masked secret fields. All layout uses lipgloss.

### Preserved Patterns

`PromptMsg` and `VerifyMsg` with their channel-based response mechanism are preserved in
`progressModel`. `ConfigStage`'s disk picker continues to use `sink.Prompt()` and
`sink.Verify()` via these messages — this pattern is correct and intentional for interactive
stage prompts and is not changed by this work.

---

## Entry Paths

### 1. No `config.yaml` (first run)
```
binary launched → config.Load() returns incomplete RunConfig
→ selectInitialScreen() returns initialModeWizard
→ AppModel starts with wizardModel ("fields" state, empty pre-fill)
→ wizard collects fields → on completion: tea.Cmd sends WizardResult on resultCh
→ main.go goroutine receives result → translates to RunConfig → validates → writes files
→ program.Send(startStagesMsg{rc}) → AppModel swaps to progressModel
→ stage runner goroutine starts
```

### 2. `config.yaml` present, no `--wizard` flag
```
binary launched → config.Load() returns complete RunConfig
→ selectInitialScreen() returns initialModePreamble
→ AppModel starts with wizardModel ("preamble" state)
  ("Found config.yaml — use it? [Y/n]")
→ Y / Enter: tea.Cmd sends WizardResult{Noop: true} on resultCh
  → main.go goroutine calls program.Send(startStagesMsg{rc: existingRC})
  → AppModel swaps to progressModel
  → stage runner goroutine starts with existing RunConfig
→ n: wizardModel transitions internal state to "fields", all fields pre-filled
  → proceeds as Entry Path 1
→ Ctrl+C: program.Quit → main.go exits code 0
```

### 3. `--wizard` flag
```
binary launched → config.Load() (file may or may not exist)
→ selectInitialScreen() returns initialModeWizard (flag overrides)
→ AppModel starts with wizardModel ("fields" state, pre-fill from loaded config)
→ user steps through fields, editing as needed
→ on completion → writes updated config.yaml + .env → transitions to progressModel
```

---

## Transition Mechanism

`main.go` creates a `chan WizardResult` (buffered 1) and passes it to `AppModel` at
construction. All wizard → progress transitions flow through a single message type:
`startStagesMsg`. There is no separate `wizardCompleteMsg`.

```go
resultCh  := make(chan config.WizardResult, 1)
mode      := selectInitialScreen(rc, flagWizard)   // in main.go
appModel  := tui.NewApp(mode, rc, resultCh)
program   := tea.NewProgram(appModel, tea.WithAltScreen())
```

When `wizardModel` completes (all fields valid, last field submitted), its `update` method
returns a `tea.Cmd` — not a raw goroutine — that sends `config.WizardResult` on `resultCh`.
There is no separate `wizardCompleteMsg`; the transition to `progressModel` is driven
entirely by `startStagesMsg`, which `main.go` sends after processing the result:

```go
// inside wizardModel.update, on completion:
return self, func() tea.Msg {
    resultCh <- config.WizardResult{Values: ..., Secrets: ..., Noop: false}
    return nil  // AppModel ignores nil msgs; transition happens via startStagesMsg
}
```

`main.go` runs a goroutine before `program.Run()`:

```go
go func() {
    result := <-resultCh
    if result.Noop {
        program.Send(startStagesMsg{rc: existingRC})
        return
    }
    rc, err := config.FromWizardResult(result)
    if err != nil {
        program.Send(fatalMsg{err})
        return
    }
    if err := config.Validate(rc); err != nil {
        program.Send(fatalMsg{err})
        return
    }
    if err := config.WriteConfigYAML(rc.Config); err != nil {
        program.Send(fatalMsg{err})
        return
    }
    if err := config.WriteEnvFile(rc.Secrets); err != nil {
        program.Send(fatalMsg{err})
        return
    }
    program.Send(startStagesMsg{rc: rc})
}()
```

`AppModel.Update` handles `startStagesMsg`: swaps `current` to a new `progressModel` and
returns a `tea.Cmd` that launches the stage runner goroutine. The goroutine sends
`StageUpdateMsg` / `LogMsg` / `PromptMsg` / `VerifyMsg` to `program` — identical to today.

`AppModel.Update` handles `fatalMsg`: renders the error in the active screen and exits.

**`Ctrl+C` exit:** Bubbletea calls `program.Quit()` on `Ctrl+C`, which causes `program.Run()`
to return. `main.go` checks the final model state and returns `nil` (exit code 0) on clean
cancellation, or a non-nil error (exit code 1) on fatal failure. No explicit `os.Exit` call.

---

## Function Relocations

`writeConfigYAML` and `writeEnvFile` currently live in `internal/stages/stage0_config.go`
as unexported functions. They move to `internal/config/config.go` as exported functions,
both writing with mode `0600`:

```go
// internal/config/config.go
func WriteConfigYAML(cfg *Config) error { ... }  // mode 0600, atomic write
func WriteEnvFile(sec *Secrets) error   { ... }  // mode 0600, atomic write
```

`ConfigStage.Run` is updated to:
- Call `config.WriteConfigYAML` (after disk picker) — kept
- **Remove** the `writeEnvFile` call entirely — secrets are written only by the wizard
  completion path in `main.go`

`main.go` calls both `WriteConfigYAML` and `WriteEnvFile` from the wizard goroutine.

**Headless re-run:** When `--headless` is used with a complete `config.yaml` and `.env`
already on disk, `config.Load()` reads both. No wizard runs. `ConfigStage` calls
`WriteConfigYAML` only after the disk picker (to persist disk fields). `WriteEnvFile` is
never called in headless mode.

---

## Package Dependency Resolution

Three structural decisions are made explicitly here to avoid import cycles and guide
implementers:

**1. `WizardResult` lives in `internal/config`, not `internal/tui`.**

```go
// internal/config/config.go
type WizardResult struct {
    Values  map[string]string // structural config fields (yaml key → value)
    Secrets map[string]string // "CF_DNS_API_TOKEN", "BOOTSTRAP_ADMIN_PASSWORD"
    Noop    bool              // true = existing config accepted at preamble
}
```

`internal/tui` imports `internal/config` (for `config.WizardResult` and
`config.RunConfig`). `internal/config` never imports `internal/tui`. This is the natural
layering: TUI is above config in the dependency graph. No import cycle.

**2. `selectInitialScreen` lives in `cmd/bootstrap/main.go`, not in `internal/tui`.**

It uses `config.Validate`, so it belongs in `main.go` where both `config` and `tui` are
already imported. `tui.NewApp` receives the resolved `initialMode` enum and a
`*config.RunConfig` — it does not call `config.Validate` itself.

**3. `wizardModel` is the sole authority for `bootstrap_server_ip` derivation.**

When field 6 is skipped (bare IP case), `wizardModel` still sets
`Values["bootstrap_server_ip"]` silently before completing. `WizardResult.Values` always
contains `bootstrap_server_ip` by the time it reaches `main.go`. `config.FromWizardResult`
reads it as a simple string mapping — no re-derivation. The derivation logic exists only
in `wizardModel`.

## `config.FromWizardResult`

```go
// internal/config/config.go
func FromWizardResult(r WizardResult) (*RunConfig, error)
```

Maps `r.Values` string keys directly to `Config` fields and `r.Secrets` to `Secrets`
fields. `bootstrap_server_ip` is read directly from `r.Values["bootstrap_server_ip"]` —
no derivation here. Returns a fully populated `*RunConfig` ready for `Validate`. This
function is exported and unit-tested independently of the TUI.

---

## Wizard Fields

Ten config fields, presented as 9–10 prompts depending on `bootstrap_server_ip` derivation.
`BOOTSTRAP_ADMIN_PASSWORD` confirm is an inline sub-step of prompt 10, not a separate prompt.
The `[N of N]` counter reflects the actual prompt count (9 or 10) determined at wizard start.

| # | Config key | Label | Smart default | Type |
|---|-----------|-------|---------------|------|
| 1 | `target_host` | Target host | — | text; e.g. `192.168.1.100` |
| 2 | `ssh_user` | SSH user | `ubuntu` | text |
| 3 | `ssh_key_path` | SSH private key | Scan `~/.ssh/id_ed25519`, `~/.ssh/id_rsa` | text + detect |
| 4 | `internal_domain` | Internal domain | — | text; e.g. `home.lab` |
| 5 | `cloudflare_zone_id` | Cloudflare Zone ID | — | text |
| 6 | `bootstrap_server_ip` | Bootstrap server IP | Derived from `target_host` | conditional |
| 7 | `lab_vlan_subnet` | Lab VLAN subnet | — | text; e.g. `10.0.0.0/24` |
| 8 | `admin_email` | Admin email | — | text |
| 9 | `CF_DNS_API_TOKEN` | Cloudflare API token | — | **masked** |
| 10 | `BOOTSTRAP_ADMIN_PASSWORD` | Admin password | — | **masked + inline confirm** |

**`bootstrap_server_ip` derivation decision tree** (evaluated after field 1 is submitted,
before field 6 is shown):

```
1. Strip port if present: host = strings.Split(target_host, ":")[0]
2. net.ParseIP(host) != nil  → use host silently as bootstrap_server_ip; skip field 6
3. net.ParseIP(host) == nil  → attempt net.LookupHost(host)
   a. Resolves to one or more IPs → pre-fill field 6 with first result; show prompt
   b. Resolution fails          → show field 6 with empty input + inline warning
                                   "Could not resolve — enter the server IP manually"
```

Field 6 is skipped (counter stays at 9) only in case 2. All other cases show field 6
(counter is 10). The total prompt count is computed once, at wizard start after field 1
is submitted, and does not change mid-run.

**`BOOTSTRAP_ADMIN_PASSWORD` confirm:** After the user enters the password and presses Enter,
a second masked input appears on the same screen ("Confirm password:"). If values do not
match, both inputs are cleared, an inline error is shown, and focus returns to the first
password input. This is not a separate wizard step.

**Optional fields** (`lab_vlan_id`, `vault_shares`, `vault_threshold`) use defaults (0, 5, 3).
The VLAN subnet field shows a static hint: "Advanced options (VLAN ID, Vault shares) can be
edited by re-running with --wizard after setup." No interactive sub-flow; the note is
display-only text, not a key binding.

**SSH key detection:** Check `~/.ssh/id_ed25519`, then `~/.ssh/id_rsa`, then
`~/.ssh/id_ed25519_sk`. If found, pre-fill the input with the detected path. If not found,
show empty input with hint "No key detected — enter path to an existing key." SSH key
generation is **deferred** to a future iteration.

---

## Per-Field Screen Layout

```
 bootstrap-home-infra ──────────────────────────────────────────

  SSH Private Key                                       [3 of 9]
  Path to the private key used to reach your server.
  Example: ~/.ssh/id_ed25519

  Detected: /Users/brian/.ssh/id_ed25519

  > /Users/brian/.ssh/id_ed25519_

  ↵ accept · esc go back
```

Components (top to bottom):
1. Shared header (`styleHeader`) — identical to progress screen
2. Field label + `[N of N]` counter (right-aligned)
3. One-line description
4. Example value (dimmed) — omitted for secret fields
5. Detected default line (if any, highlighted) — omitted when no default
6. Text input (focused, cursor visible); `EchoModePassword` for secret fields
7. Inline validation error (red, below input) — only shown when present
8. Available actions hint line (dimmed)

---

## Navigation

| Key | Action |
|-----|--------|
| `Enter` | Accept value, validate inline, advance to next field |
| `Esc` | Go back to previous field (current value preserved); no-op on first field |
| `Ctrl+C` | Cancel wizard entirely — no files written, `program.Quit()`, exit code 0 |

Inline validation fires on `Enter`. If invalid, error appears below the input and the same
field is re-presented. The user never advances past a field with a failing validation.

---

## Preamble Screen

`wizardModel` has two internal states: `"preamble"` and `"fields"`. The preamble is the
initial state when `selectInitialScreen` returns `initialModePreamble`. It is rendered by
`wizardModel.view()` — it is not a separate screen implementation.

```
 bootstrap-home-infra ──────────────────────────────────────────

  Found config.yaml

  target_host:     192.168.1.100
  ssh_user:        ubuntu
  internal_domain: home.lab
  admin_email:     admin@example.com
  ...

  Use this config and proceed?  [Y/n]
```

`Y` or `Enter` → `wizardModel.update` returns a `tea.Cmd` sending `WizardResult{Noop: true}`
on `resultCh`. `main.go` goroutine calls `program.Send(startStagesMsg{rc: existingRC})`.
`AppModel` swaps to `progressModel`.

`n` → `wizardModel` sets internal state to `"fields"`, pre-fills all values from loaded
config, returns `self` (no screen swap).

`Ctrl+C` → `program.Quit()`, exit code 0.

---

## Data Flow

```
WizardResult (defined in internal/config):
    Values  map[string]string   // structural config field values by yaml key
    Secrets map[string]string   // "CF_DNS_API_TOKEN", "BOOTSTRAP_ADMIN_PASSWORD"
    Noop    bool                // true = existing config accepted, skip file writes

main.go goroutine on receive:
  Noop=true  → program.Send(startStagesMsg{rc: existingRC})
  Noop=false →
    1. config.FromWizardResult(result) → *RunConfig  [incl. bootstrap_server_ip derivation]
    2. config.Validate(rc)             → fatalMsg on error
    3. config.WriteConfigYAML(cfg)     → atomic (temp+rename), mode 0600; fatalMsg on error
    4. config.WriteEnvFile(sec)        → atomic (temp+rename), mode 0600; fatalMsg on error
    5. program.Send(startStagesMsg{rc: rc})
```

`wizardModel` never imports `config`. The `tui` package gains no new imports.

**fatalMsg handling:** `AppModel.Update` renders the error text on whatever screen is
currently active, then returns `tea.Quit`. The binary exits with a non-zero code derived
from the error return of `program.Run()`.

---

## ConfigStage Changes

`ConfigStage` (`"config-collection"`) loses field collection and secret writing.
Remaining responsibilities:
- Call `config.Validate(rc)` — sanity check on already-complete RunConfig
- Test SSH connectivity
- Detect target arch and OS
- Run disk picker (via `sink.Prompt` / `sink.Verify` — unchanged)
- Call `config.WriteConfigYAML(rc.Config)` after disk picker persists disk fields
- Write run manifest

**Explicit removal:** The `writeEnvFile(cfg.Secrets)` call at line 101–103 of
`stage0_config.go` is removed. `ConfigStage` must never write the `.env` file.

---

## Model / Constructor Renames

| Before | After | Notes |
|--------|-------|-------|
| `tui.Model` (exported) | `progressModel` (unexported) | Used only within `tui` package |
| `tui.New(stages, logPath)` | `tui.NewProgressModel(stages, logPath)` | Called by `tui.NewApp` internally |
| — | `tui.NewApp(mode initialMode, preloadedRC *config.RunConfig, resultCh chan config.WizardResult) AppModel` | Called by `main.go`; `selectInitialScreen` runs in `main.go` before this call |

`main.go` calls `tui.NewApp(...)` — it no longer calls `tui.New(...)` directly.
`NewApp` constructs the appropriate initial screen (`wizardModel` or direct `progressModel`)
and wires `resultCh`.

Exported message types (`StageStatus`, `LogMsg`, `StageUpdateMsg`, `PromptMsg`, `VerifyMsg`,
`WarningBoxMsg`) remain exported from the `tui` package, unchanged. Callers in
`internal/stage` and `main.go` need no changes to these types.

---

## `--wizard` Flag

```go
rootCmd.Flags().BoolVar(&flagWizard, "wizard", false,
    "Run setup wizard (pre-fills from existing config if present)")
```

When set, `selectInitialScreen` always returns `initialModeWizard`.

---

## Entry Path Selection (Testable Extraction)

```go
// internal/tui/app.go  (or cmd/bootstrap/main.go)
type initialMode int
const (
    initialModeWizard   initialMode = iota // show wizard fields
    initialModePreamble                    // show "use existing config?" prompt
)

func selectInitialScreen(rc *config.RunConfig, wizardFlag bool) initialMode {
    if wizardFlag {
        return initialModeWizard
    }
    if config.Validate(rc) != nil {
        return initialModeWizard
    }
    return initialModePreamble
}
```

This function is unit-tested independently of the TUI with no `tea.Program` required.

---

## Error Handling

| Scenario | Behaviour |
|----------|-----------|
| `Ctrl+C` at any point | `program.Quit()`, no files written, exit code 0 |
| Inline field validation failure | Error shown below input, same field re-presented |
| DNS resolution failure (`target_host`) | Inline warning on field 6, user enters IP manually |
| Password confirm mismatch | Both inputs cleared, error shown, focus → first password input |
| `config.Validate` fails after `FromWizardResult` | `fatalMsg` sent; error shown on wizard screen; exit code 1 |
| `WriteConfigYAML` fails | `fatalMsg`; error on wizard screen; exit code 1 |
| `WriteEnvFile` fails | `fatalMsg`; error on wizard screen; exit code 1 |
| Write permission denied | Caught by `WriteConfigYAML` or `WriteEnvFile` fail path above |

---

## Testing

Run all tests: `go test ./...`
Run a specific package: `go test ./internal/config/...` or `go test ./internal/tui/...`

Unit tests for `wizardModel` and `AppModel` live in `package tui` (not `package tui_test`)
to access unexported types. All other tests are black-box (`package foo_test`).
No `tea.NewProgram` is needed for any unit test — call `Update()` / `View()` directly.

---

### `cmd/bootstrap/main_test.go` — `package main`

**`TestSelectInitialScreen`** — table-driven:

```go
tests := []struct {
    name       string
    rc         *config.RunConfig  // fields set or empty
    wizardFlag bool
    want       initialMode
}{
    {"no config fields, no flag",    emptyRC(),    false, initialModeWizard},
    {"complete config, no flag",     completeRC(), false, initialModePreamble},
    {"complete config, wizard flag", completeRC(), true,  initialModeWizard},
    {"incomplete config, no flag",   partialRC(),  false, initialModeWizard},
    {"empty config, wizard flag",    emptyRC(),    true,  initialModeWizard},
}
```

Pass: returned `initialMode` matches `want`. No TUI started.

---

### `internal/config/wizard_test.go` — `package config_test`

**`TestFromWizardResult`** — table-driven, one row per field mapping:

```go
// Minimal valid WizardResult for a non-hostname target (field 6 pre-set by wizardModel)
base := config.WizardResult{
    Values: map[string]string{
        "target_host":        "192.168.1.50",
        "ssh_user":           "ubuntu",
        "ssh_key_path":       "/home/user/.ssh/id_ed25519",
        "internal_domain":    "home.lab",
        "cloudflare_zone_id": "abc123",
        "bootstrap_server_ip": "192.168.1.50",
        "lab_vlan_subnet":    "10.0.0.0/24",
        "admin_email":        "admin@example.com",
    },
    Secrets: map[string]string{
        "CF_DNS_API_TOKEN":          "tok-abc",
        "BOOTSTRAP_ADMIN_PASSWORD":  "hunter2",
    },
}
rc, err := config.FromWizardResult(base)
// assert: err == nil
// assert: rc.TargetHost == "192.168.1.50"
// assert: rc.BootstrapServerIP == "192.168.1.50"
// assert: rc.SSHUser == "ubuntu"
// ... one assertion per field
// assert: rc.CloudflareAPIToken == "tok-abc"
// assert: rc.AdminPassword == "hunter2"
```

**`TestFromWizardResult_MissingField`**: omit one required key at a time; assert `err != nil`.

---

### `internal/config/bootstrap_ip_test.go` — `package config_test`

**`TestDeriveBootstrapServerIP`** — table-driven (covers the decision tree in full):

```go
tests := []struct {
    targetHost string
    wantIP     string   // empty = expect prompt (not derived silently)
    wantSkip   bool     // true = field 6 skipped, IP set silently
}{
    {"192.168.1.100",       "192.168.1.100", true},   // bare IPv4 → silent
    {"192.168.1.100:22",    "192.168.1.100", true},   // IP+port → strip port, silent
    {"192.168.1.100:2222",  "192.168.1.100", true},   // IP+non-standard port → silent
    {"localhost",           "127.0.0.1",     false},  // resolvable hostname → pre-fill, show
    {"doesnotexist.invalid","",              false},  // unresolvable → empty, show warning
}
```

Pass: `wantSkip` matches whether field 6 was skipped; `wantIP` matches the derived value
(empty string means the field was shown with no pre-fill).

---

### `internal/config/write_test.go` — `package config_test`

**`TestWriteConfigYAML`**:
```go
dir := t.TempDir()
cfg := completeConfig()
err := config.WriteConfigYAMLTo(dir+"/config.yaml", cfg)
// assert: err == nil
fi, _ := os.Stat(dir + "/config.yaml")
// assert: fi.Mode().Perm() == 0600
data, _ := os.ReadFile(dir + "/config.yaml")
// assert: strings.Contains(string(data), `target_host: "192.168.1.100"`)
// assert: no temp file left in dir (atomic write completed)
```

**`TestWriteConfigYAML_PermissionDenied`**: pass a read-only directory; assert `err != nil`.

**`TestWriteEnvFile`** — same structure: write, check mode 0600, check contents
(`CF_DNS_API_TOKEN=...`, `BOOTSTRAP_ADMIN_PASSWORD=...`), no temp file left.

**`TestWriteEnvFile_PermissionDenied`**: read-only dir; assert `err != nil`.

Note: `WriteConfigYAML` and `WriteEnvFile` must accept an explicit path parameter
(not hardcoded `"config.yaml"`) to be testable with `t.TempDir()`. The production
call site in `main.go` passes `flagConfig` and `flagEnv` as the paths.

---

### `internal/tui/wizard_test.go` — `package tui`

**`TestWizardModel_FieldAdvance`**:
```go
m := newWizardModel(initialModeWizard, emptyPreload())
// Simulate typing a value and pressing Enter for field 1
m2, _ := m.update(typeText("192.168.1.100"))
m3, _ := m2.update(pressEnter())
// assert: m3.cursor == 1  (advanced to field 2)
// assert: m3.values["target_host"] == "192.168.1.100"
```

**`TestWizardModel_BackNavigation`**:
```go
// Advance to field 2, then press Esc
m := advanceTo(2)
m2, _ := m.update(pressEsc())
// assert: m2.cursor == 1  (back to field 1... wait, 0-indexed: back to field 0)
// assert: m2.values["ssh_user"] preserved from prior entry
```

**`TestWizardModel_ValidationError`**:
```go
m := advanceTo(0)  // target_host field
m2, _ := m.update(typeText("not-valid!!"))
m3, _ := m2.update(pressEnter())
// assert: m3.cursor == 0  (did not advance)
// assert: m3.errMsg != ""
```

**`TestWizardModel_PasswordMasked`**:
```go
m := advanceTo(8)  // CF_DNS_API_TOKEN field (0-indexed: 8)
// assert: m.input.EchoMode == textinput.EchoPassword
```

**`TestWizardModel_PasswordConfirmMismatch`**:
```go
m := advanceTo(9)  // BOOTSTRAP_ADMIN_PASSWORD
m2, _ := m.update(typeText("password1"))
m3, _ := m2.update(pressEnter())  // submit first entry → confirm sub-step appears
m4, _ := m3.update(typeText("password2"))
m5, _ := m4.update(pressEnter())  // submit confirm → mismatch
// assert: m5.errMsg contains "do not match"
// assert: both password inputs cleared (m5.input.Value() == "")
// assert: m5.cursor still on field 9 (password field, not advanced)
```

**`TestWizardModel_PreambleAccept`**:
```go
m := newWizardModel(initialModePreamble, completePreload())
// assert: m.state == "preamble"
_, cmd := m.update(pressEnter())
// execute cmd and capture message
msg := cmd()
// assert: msg is a tea.Cmd that sends WizardResult{Noop: true} (or assert on resultCh)
```

**`TestWizardModel_PreambleReject`**:
```go
m := newWizardModel(initialModePreamble, completePreload())
m2, _ := m.update(typeKey("n"))
// assert: m2.state == "fields"
// assert: m2.cursor == 0
// assert: m2.values["target_host"] == completePreload().Values["target_host"]
```

---

### `internal/tui/app_test.go` — `package tui`

**`TestAppModel_WizardToProgress`**:
```go
resultCh := make(chan config.WizardResult, 1)
app := NewApp(initialModeWizard, emptyRC(), resultCh)
app, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
// Send startStagesMsg (simulating main.go goroutine after receiving WizardResult)
app2, _ := app.Update(startStagesMsg{rc: completeRC()})
// assert: app2.current is a *progressModel (not *wizardModel)
```

**`TestAppModel_FatalMsg`**:
```go
app := NewApp(initialModeWizard, emptyRC(), make(chan config.WizardResult, 1))
app, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
app2, cmd := app.Update(fatalMsg{errors.New("disk write failed")})
// assert: app2.View() contains "disk write failed"
// assert: cmd == tea.Quit (or executing cmd produces tea.QuitMsg)
```

**`TestAppModel_ScreenSwapCallsInit`**:
```go
// Confirm that when current.update returns a different screen,
// AppModel calls init() on the new screen and batches cmds.
// Use a test double for screen that records init() calls.
```

---

### `internal/tui/sshkey_test.go` — `package tui`

**`TestDetectSSHKey`**:
```go
dir := t.TempDir()
// Case 1: id_ed25519 present
os.WriteFile(dir+"/id_ed25519", []byte("fake key"), 0600)
got := detectSSHKey(dir)
// assert: got == dir+"/id_ed25519"

// Case 2: only id_rsa present
got2 := detectSSHKey(dir + "/empty")
// assert: got2 == ""  (not found)
```

---

### Regression: Headless mode unaffected

**`TestHeadlessStillWorks`** (in `internal/stages/stage0_config_test.go`):

Verify that `ConfigStage.Run` no longer calls `WriteEnvFile`. After the refactor, run
`ConfigStage` against a mock sink with a complete `RunConfig` and assert:
- `WriteEnvFile` is NOT called (use a spy or check `.env` is not written)
- `WriteConfigYAML` IS called after the disk picker

This protects against reintroducing secret writes in headless mode.
