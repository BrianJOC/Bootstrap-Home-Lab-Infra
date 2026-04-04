# First-Run Setup Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the broken first-run experience (errors on missing config) with a guided sequential setup wizard that collects all required configuration, writes `config.yaml` and `.env` atomically, and transitions seamlessly into the stage runner dashboard.

**Architecture:** Screen router pattern — one `tea.Program`, one `AppModel` delegating to an active `screen` interface. `wizardModel` (sequential field form) and `progressModel` (existing stage runner, renamed from `Model`) are the two screens. All wizard→progress transitions flow through a single `StartStagesMsg` via a buffered `chan config.WizardResult`.

**Tech Stack:** Go 1.22, Bubbletea v1.3.10, Lipgloss v1.1.0, charmbracelet/bubbles v1.0.0 (textinput already imported), cobra, zap.

**Spec:** `docs/superpowers/specs/2026-04-03-first-run-wizard-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/config/wizard.go` | `WizardResult`, `DeriveBootstrapServerIP`, `FromWizardResult` |
| Create | `internal/config/write.go` | Exported `WriteConfigYAML(path, cfg)` and `WriteEnvFile(path, sec)` with atomic writes |
| Modify | `internal/stages/stage0_config.go` | Remove unexported `writeConfigYAML`/`writeEnvFile`; call exported versions; remove `writeEnvFile` call |
| Create | `internal/tui/app.go` | `screen` interface, `AppModel`, `InitialMode` enum, `FatalMsg`, `StartStagesMsg`, `NewApp` |
| Create | `internal/tui/tui_testhelpers_test.go` | Shared test helpers (`emptyRC`, `completeRC`) used by both `app_test.go` and `wizard_test.go` |
| Modify | `internal/tui/model.go` | Rename `Model`→`progressModel` (unexported), `New`→`newProgressModel`; add `screen` interface methods |
| Create | `internal/tui/sshkey.go` | `detectSSHKey(sshDir string) string` helper |
| Create | `internal/tui/wizard.go` | `wizardModel` — preamble + field navigation + password confirm + WizardResult emission |
| Modify | `cmd/bootstrap/main.go` | Add `--wizard` flag, `selectInitialScreen`, update `runTUI` to use `NewApp` and `resultCh` goroutine |
| Create | `internal/config/wizard_test.go` | `TestFromWizardResult`, `TestFromWizardResult_MissingField` |
| Create | `internal/config/bootstrap_ip_test.go` | `TestDeriveBootstrapServerIP` (full decision tree) |
| Create | `internal/config/write_test.go` | `TestWriteConfigYAML`, `TestWriteEnvFile`, permission-denied cases |
| Create | `internal/tui/sshkey_test.go` | `TestDetectSSHKey` |
| Create | `internal/tui/wizard_test.go` | Field advance, back nav, validation error, password confirm, preamble states |
| Create | `internal/tui/app_test.go` | Screen swap, FatalMsg, WizardToProgress transition |
| Create | `cmd/bootstrap/main_test.go` | `TestSelectInitialScreen` table-driven |

---

## Task 1: `WizardResult`, `DeriveBootstrapServerIP`, and `FromWizardResult`

**Files:**
- Create: `internal/config/wizard.go`
- Create: `internal/config/wizard_test.go`
- Create: `internal/config/bootstrap_ip_test.go`

- [ ] **Step 1: Write failing tests for `DeriveBootstrapServerIP`**

```go
// internal/config/bootstrap_ip_test.go
package config_test

import (
    "testing"
    "github.com/homelab/bootstrap-home-infra/internal/config"
)

func TestDeriveBootstrapServerIP(t *testing.T) {
    tests := []struct {
        name       string
        targetHost string
        wantIP     string
        wantSkip   bool // true = IP derived silently, field 6 skipped
    }{
        {"bare IPv4",            "192.168.1.100",      "192.168.1.100", true},
        {"IPv4 with port",       "192.168.1.100:22",   "192.168.1.100", true},
        {"IPv4 non-std port",    "192.168.1.100:2222", "192.168.1.100", true},
        {"resolvable hostname",  "localhost",           "127.0.0.1",     false},
        {"unresolvable host",    "doesnotexist.invalid","",              false},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            ip, skip := config.DeriveBootstrapServerIP(tc.targetHost)
            if skip != tc.wantSkip {
                t.Errorf("skip=%v want %v", skip, tc.wantSkip)
            }
            if tc.wantIP != "" && ip != tc.wantIP {
                t.Errorf("ip=%q want %q", ip, tc.wantIP)
            }
        })
    }
}
```

```go
// internal/config/wizard_test.go
package config_test

import (
    "testing"
    "github.com/homelab/bootstrap-home-infra/internal/config"
)

func baseWizardResult() config.WizardResult {
    return config.WizardResult{
        Values: map[string]string{
            "target_host":         "192.168.1.50",
            "ssh_user":            "ubuntu",
            "ssh_key_path":        "/home/user/.ssh/id_ed25519",
            "internal_domain":     "home.lab",
            "cloudflare_zone_id":  "abc123",
            "bootstrap_server_ip": "192.168.1.50",
            "lab_vlan_subnet":     "10.0.0.0/24",
            "admin_email":         "admin@example.com",
        },
        Secrets: map[string]string{
            "CF_DNS_API_TOKEN":         "tok-abc",
            "BOOTSTRAP_ADMIN_PASSWORD": "hunter2",
        },
    }
}

func TestFromWizardResult(t *testing.T) {
    r := baseWizardResult()
    rc, err := config.FromWizardResult(r)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    tests := []struct{ name, got, want string }{
        {"TargetHost",        rc.TargetHost,        "192.168.1.50"},
        {"SSHUser",           rc.SSHUser,           "ubuntu"},
        {"SSHKeyPath",        rc.SSHKeyPath,        "/home/user/.ssh/id_ed25519"},
        {"InternalDomain",    rc.InternalDomain,    "home.lab"},
        {"CloudflareZoneID",  rc.CloudflareZoneID,  "abc123"},
        {"BootstrapServerIP", rc.BootstrapServerIP, "192.168.1.50"},
        {"LabVLANSubnet",     rc.LabVLANSubnet,     "10.0.0.0/24"},
        {"AdminEmail",        rc.AdminEmail,        "admin@example.com"},
        {"CloudflareToken",   rc.Secrets.CloudflareAPIToken, "tok-abc"},
        {"AdminPassword",     rc.Secrets.AdminPassword,      "hunter2"},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            if tc.got != tc.want {
                t.Errorf("got %q, want %q", tc.got, tc.want)
            }
        })
    }
    if rc.VaultShares != 5 {
        t.Errorf("VaultShares default = %d, want 5", rc.VaultShares)
    }
    if rc.VaultThreshold != 3 {
        t.Errorf("VaultThreshold default = %d, want 3", rc.VaultThreshold)
    }
}

func TestFromWizardResult_MissingField(t *testing.T) {
    required := []string{
        "target_host", "ssh_user", "ssh_key_path", "internal_domain",
        "cloudflare_zone_id", "bootstrap_server_ip", "lab_vlan_subnet", "admin_email",
    }
    for _, key := range required {
        t.Run("missing_"+key, func(t *testing.T) {
            r := baseWizardResult()
            delete(r.Values, key)
            _, err := config.FromWizardResult(r)
            if err == nil {
                t.Errorf("expected error for missing key %q", key)
            }
        })
    }
    for _, key := range []string{"CF_DNS_API_TOKEN", "BOOTSTRAP_ADMIN_PASSWORD"} {
        t.Run("missing_secret_"+key, func(t *testing.T) {
            r := baseWizardResult()
            delete(r.Secrets, key)
            _, err := config.FromWizardResult(r)
            if err == nil {
                t.Errorf("expected error for missing secret %q", key)
            }
        })
    }
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
go test ./internal/config/... -run 'TestDeriveBootstrapServerIP|TestFromWizardResult'
```
Expected: compile error (types not defined).

- [ ] **Step 3: Implement `wizard.go`**

```go
// internal/config/wizard.go
package config

import (
    "fmt"
    "net"
    "strings"
)

// WizardResult is the output of the first-run wizard.
// Defined in internal/config so internal/tui can import it without creating a cycle.
type WizardResult struct {
    Values  map[string]string // structural config fields by yaml key
    Secrets map[string]string // "CF_DNS_API_TOKEN", "BOOTSTRAP_ADMIN_PASSWORD"
    Noop    bool              // true = existing config accepted at preamble; skip file writes
}

// DeriveBootstrapServerIP implements the bootstrap_server_ip decision tree.
// Returns (ip, skip):
//   - skip=true:  IP derived silently from a bare IP; field 6 should be skipped
//   - skip=false: IP may be pre-filled (DNS) or empty (unresolvable); field 6 shown
func DeriveBootstrapServerIP(targetHost string) (ip string, skip bool) {
    host := strings.Split(targetHost, ":")[0]
    if parsed := net.ParseIP(host); parsed != nil {
        return parsed.String(), true
    }
    addrs, err := net.LookupHost(host)
    if err != nil || len(addrs) == 0 {
        return "", false
    }
    return addrs[0], false
}

// FromWizardResult maps a completed WizardResult to a *RunConfig.
// bootstrap_server_ip is read directly from r.Values — wizardModel is the
// sole authority for derivation; this function performs no re-derivation.
func FromWizardResult(r WizardResult) (*RunConfig, error) {
    mustGet := func(key string) (string, error) {
        v, ok := r.Values[key]
        if !ok || strings.TrimSpace(v) == "" {
            return "", fmt.Errorf("missing required field %q in WizardResult.Values", key)
        }
        return v, nil
    }
    mustSecret := func(key string) (string, error) {
        v, ok := r.Secrets[key]
        if !ok || strings.TrimSpace(v) == "" {
            return "", fmt.Errorf("missing required secret %q in WizardResult.Secrets", key)
        }
        return v, nil
    }

    targetHost, err := mustGet("target_host")
    if err != nil { return nil, err }
    sshUser, err := mustGet("ssh_user")
    if err != nil { return nil, err }
    sshKeyPath, err := mustGet("ssh_key_path")
    if err != nil { return nil, err }
    internalDomain, err := mustGet("internal_domain")
    if err != nil { return nil, err }
    cfZoneID, err := mustGet("cloudflare_zone_id")
    if err != nil { return nil, err }
    bootstrapIP, err := mustGet("bootstrap_server_ip")
    if err != nil { return nil, err }
    labVLANSubnet, err := mustGet("lab_vlan_subnet")
    if err != nil { return nil, err }
    adminEmail, err := mustGet("admin_email")
    if err != nil { return nil, err }

    cfToken, err := mustSecret("CF_DNS_API_TOKEN")
    if err != nil { return nil, err }
    adminPass, err := mustSecret("BOOTSTRAP_ADMIN_PASSWORD")
    if err != nil { return nil, err }

    cfg := &Config{
        TargetHost:        targetHost,
        SSHUser:           sshUser,
        SSHKeyPath:        sshKeyPath,
        InternalDomain:    internalDomain,
        CloudflareZoneID:  cfZoneID,
        BootstrapServerIP: bootstrapIP,
        LabVLANSubnet:     labVLANSubnet,
        AdminEmail:        adminEmail,
        VaultShares:       5,
        VaultThreshold:    3,
    }
    if v := r.Values["lab_vlan_id"]; v != "" {
        fmt.Sscanf(v, "%d", &cfg.LabVLANID)
    }

    return &RunConfig{
        Config: cfg,
        Secrets: &Secrets{
            CloudflareAPIToken: cfToken,
            AdminPassword:      adminPass,
        },
    }, nil
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
go test ./internal/config/... -run 'TestDeriveBootstrapServerIP|TestFromWizardResult'
```
Expected: PASS.

- [ ] **Step 5: Run all config tests**

```
go test ./internal/config/...
```
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/config/wizard.go internal/config/wizard_test.go internal/config/bootstrap_ip_test.go
git commit -m "feat(config): add WizardResult, DeriveBootstrapServerIP, FromWizardResult"
```

---

## Task 2: Exported `WriteConfigYAML` and `WriteEnvFile` with atomic writes

**Files:**
- Create: `internal/config/write.go`
- Create: `internal/config/write_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/config/write_test.go
package config_test

import (
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/homelab/bootstrap-home-infra/internal/config"
)

func completeConfig() *config.Config {
    return &config.Config{
        TargetHost:        "192.168.1.100",
        SSHUser:           "ubuntu",
        SSHKeyPath:        "/home/user/.ssh/id_ed25519",
        InternalDomain:    "home.lab",
        CloudflareZoneID:  "abc123",
        BootstrapServerIP: "192.168.1.100",
        LabVLANSubnet:     "10.0.0.0/24",
        AdminEmail:        "admin@example.com",
        VaultShares:       5,
        VaultThreshold:    3,
    }
}

func TestWriteConfigYAML(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "config.yaml")

    if err := config.WriteConfigYAML(path, completeConfig()); err != nil {
        t.Fatalf("WriteConfigYAML: %v", err)
    }

    fi, err := os.Stat(path)
    if err != nil {
        t.Fatalf("stat: %v", err)
    }
    if fi.Mode().Perm() != 0600 {
        t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
    }

    data, _ := os.ReadFile(path)
    content := string(data)
    for _, want := range []string{
        `target_host: "192.168.1.100"`,
        `ssh_user: "ubuntu"`,
        `internal_domain: "home.lab"`,
        `bootstrap_server_ip: "192.168.1.100"`,
    } {
        if !strings.Contains(content, want) {
            t.Errorf("config.yaml missing %q\ngot:\n%s", want, content)
        }
    }

    // No temp file left over (atomic write completed).
    entries, _ := os.ReadDir(dir)
    for _, e := range entries {
        if strings.HasSuffix(e.Name(), ".tmp") {
            t.Errorf("temp file left: %s", e.Name())
        }
    }
}

func TestWriteConfigYAML_PermissionDenied(t *testing.T) {
    dir := t.TempDir()
    if err := os.Chmod(dir, 0555); err != nil {
        t.Skip("cannot set dir permissions:", err)
    }
    t.Cleanup(func() { os.Chmod(dir, 0755) })
    err := config.WriteConfigYAML(filepath.Join(dir, "config.yaml"), completeConfig())
    if err == nil {
        t.Error("expected error for read-only dir, got nil")
    }
}

func TestWriteEnvFile(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, ".env")
    sec := &config.Secrets{
        CloudflareAPIToken: "tok-xyz",
        AdminPassword:      "s3cr3t",
    }

    if err := config.WriteEnvFile(path, sec); err != nil {
        t.Fatalf("WriteEnvFile: %v", err)
    }

    fi, err := os.Stat(path)
    if err != nil {
        t.Fatalf("stat: %v", err)
    }
    if fi.Mode().Perm() != 0600 {
        t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
    }

    data, _ := os.ReadFile(path)
    content := string(data)
    if !strings.Contains(content, "CF_DNS_API_TOKEN=tok-xyz") {
        t.Errorf("missing CF_DNS_API_TOKEN, got:\n%s", content)
    }
    if !strings.Contains(content, "BOOTSTRAP_ADMIN_PASSWORD=s3cr3t") {
        t.Errorf("missing BOOTSTRAP_ADMIN_PASSWORD, got:\n%s", content)
    }

    entries, _ := os.ReadDir(dir)
    for _, e := range entries {
        if strings.HasSuffix(e.Name(), ".tmp") {
            t.Errorf("temp file left: %s", e.Name())
        }
    }
}

func TestWriteEnvFile_PermissionDenied(t *testing.T) {
    dir := t.TempDir()
    if err := os.Chmod(dir, 0555); err != nil {
        t.Skip("cannot set dir permissions:", err)
    }
    t.Cleanup(func() { os.Chmod(dir, 0755) })
    err := config.WriteEnvFile(filepath.Join(dir, ".env"), &config.Secrets{})
    if err == nil {
        t.Error("expected error for read-only dir, got nil")
    }
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
go test ./internal/config/... -run 'TestWriteConfig|TestWriteEnv'
```
Expected: compile error (functions not defined).

- [ ] **Step 3: Implement `write.go`**

```go
// internal/config/write.go
package config

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
)

// WriteConfigYAML writes structural config fields (no secrets) to path atomically.
// Uses temp file + rename. Mode 0600. Accepts explicit path for testability.
func WriteConfigYAML(path string, cfg *Config) error {
    lines := []string{
        fmt.Sprintf("target_host: %q", cfg.TargetHost),
        fmt.Sprintf("ssh_user: %q", cfg.SSHUser),
        fmt.Sprintf("ssh_key_path: %q", cfg.SSHKeyPath),
        fmt.Sprintf("internal_domain: %q", cfg.InternalDomain),
        fmt.Sprintf("cloudflare_zone_id: %q", cfg.CloudflareZoneID),
        fmt.Sprintf("bootstrap_server_ip: %q", cfg.BootstrapServerIP),
        fmt.Sprintf("lab_vlan_subnet: %q", cfg.LabVLANSubnet),
        fmt.Sprintf("lab_vlan_id: %d", cfg.LabVLANID),
        fmt.Sprintf("admin_email: %q", cfg.AdminEmail),
        fmt.Sprintf("vault_shares: %d", cfg.VaultShares),
        fmt.Sprintf("vault_threshold: %d", cfg.VaultThreshold),
    }
    if cfg.DataDisk != "" {
        lines = append(lines, fmt.Sprintf("data_disk: %q", cfg.DataDisk))
        if cfg.DataDiskMode != "" {
            lines = append(lines, fmt.Sprintf("data_disk_mode: %q", cfg.DataDiskMode))
        }
        if cfg.DataDiskForceFormat {
            lines = append(lines, fmt.Sprintf("data_disk_force_format: %v", cfg.DataDiskForceFormat))
        }
    }
    return atomicWrite(path, strings.Join(lines, "\n")+"\n")
}

// WriteEnvFile writes secrets to path atomically (mode 0600).
func WriteEnvFile(path string, sec *Secrets) error {
    content := fmt.Sprintf("CF_DNS_API_TOKEN=%s\nBOOTSTRAP_ADMIN_PASSWORD=%s\n",
        sec.CloudflareAPIToken, sec.AdminPassword)
    return atomicWrite(path, content)
}

// atomicWrite writes content to path via temp file + rename to avoid partial writes.
func atomicWrite(path, content string) error {
    dir := filepath.Dir(path)
    tmp, err := os.CreateTemp(dir, filepath.Base(path)+"*.tmp")
    if err != nil {
        return fmt.Errorf("creating temp file: %w", err)
    }
    tmpName := tmp.Name()
    defer func() {
        tmp.Close()
        os.Remove(tmpName) // no-op if rename succeeded
    }()
    if err := tmp.Chmod(0600); err != nil {
        return fmt.Errorf("chmod temp file: %w", err)
    }
    if _, err := tmp.WriteString(content); err != nil {
        return fmt.Errorf("writing temp file: %w", err)
    }
    if err := tmp.Close(); err != nil {
        return fmt.Errorf("closing temp file: %w", err)
    }
    if err := os.Rename(tmpName, path); err != nil {
        return fmt.Errorf("renaming %s → %s: %w", tmpName, path, err)
    }
    return nil
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
go test ./internal/config/... -run 'TestWriteConfig|TestWriteEnv'
```
Expected: PASS.

- [ ] **Step 5: Run all config tests**

```
go test ./internal/config/...
```
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/config/write.go internal/config/write_test.go
git commit -m "feat(config): add exported WriteConfigYAML and WriteEnvFile with atomic writes"
```

---

## Task 3: Remove unexported write functions; update `stage0_config.go`

**Files:**
- Modify: `internal/stages/stage0_config.go`

This task removes the old unexported `writeConfigYAML` and `writeEnvFile` from
`stage0_config.go`, replaces the `writeConfigYAML` call with `config.WriteConfigYAML`,
and removes the `writeEnvFile` call entirely. A regression test guards against
re-introduction of `.env` writing in ConfigStage.

- [ ] **Step 1: Write a regression test confirming ConfigStage does NOT write `.env`**

Add this test to `internal/stages/` (create `stage0_config_test.go` if absent):

```go
// internal/stages/stage0_config_test.go
package stages_test

import (
    "os"
    "path/filepath"
    "testing"
)

// TestConfigStageDoesNotWriteEnv guards against re-introducing the .env write
// inside ConfigStage. The wizard path (main.go) is the sole writer of .env.
func TestConfigStageDoesNotWriteEnv(t *testing.T) {
    // Grep the source for writeEnvFile calls inside stage0_config.go.
    src, err := os.ReadFile(filepath.Join("..", "..", "internal", "stages", "stage0_config.go"))
    if err != nil {
        // Try relative path from test location.
        src, err = os.ReadFile("stage0_config.go")
        if err != nil {
            t.Skip("cannot read stage0_config.go:", err)
        }
    }
    if contains(string(src), "writeEnvFile") || contains(string(src), "WriteEnvFile") {
        t.Error("stage0_config.go must not contain writeEnvFile or WriteEnvFile calls; " +
            ".env is written only by the wizard path in main.go")
    }
}

func contains(s, sub string) bool {
    return len(s) >= len(sub) && (s == sub ||
        func() bool {
            for i := 0; i <= len(s)-len(sub); i++ {
                if s[i:i+len(sub)] == sub {
                    return true
                }
            }
            return false
        }())
}
```

> **Simpler approach:** Just use `strings.Contains` in the test. The above manual
> implementation avoids an extra import. Or just `import "strings"` and use
> `strings.Contains`. Either is fine.

- [ ] **Step 2: Run this regression test to confirm it initially FAILS (the code still has writeEnvFile)**

```
go test ./internal/stages/... -run TestConfigStageDoesNotWriteEnv
```
Expected: FAIL (writeEnvFile currently present).

- [ ] **Step 3: Delete the `writeConfigYAML` function from `stage0_config.go` (lines 116–144)**

Remove the entire unexported `writeConfigYAML` function.

- [ ] **Step 4: Delete the `writeEnvFile` function from `stage0_config.go` (lines 146–168)**

Remove the entire unexported `writeEnvFile` function.

- [ ] **Step 5: Replace the `writeConfigYAML` call in `ConfigStage.Run`**

Replace:
```go
    if err := writeConfigYAML(cfg.Config); err != nil {
        return fmt.Errorf("writing config.yaml: %w", err)
    }
```
With:
```go
    if err := config.WriteConfigYAML("config.yaml", cfg.Config); err != nil {
        return fmt.Errorf("writing config.yaml: %w", err)
    }
```

- [ ] **Step 6: Remove the `writeEnvFile` block from `ConfigStage.Run` (lines 99–103)**

Delete:
```go
    // Write .env with mode 0600.
    sink.Info(s.Name(), "writing .env")
    if err := writeEnvFile(cfg.Secrets); err != nil {
        return fmt.Errorf("writing .env: %w", err)
    }
```

- [ ] **Step 7: Remove unused imports from `stage0_config.go`**

After deleting the functions, check if `"os"` is still used elsewhere in the file.
It's used in `writeManifest` (`os.MkdirAll`, `os.Getpid`, `os.WriteFile`) — keep it.

- [ ] **Step 8: Verify the codebase compiles**

```
go build ./...
```
Expected: no errors.

- [ ] **Step 9: Run all tests including the new regression test**

```
go test ./...
```
Expected: all pass (including `TestConfigStageDoesNotWriteEnv`).

- [ ] **Step 10: Commit**

```bash
git add internal/stages/stage0_config.go internal/stages/stage0_config_test.go
git commit -m "refactor(stages): use config.WriteConfigYAML; remove writeEnvFile from ConfigStage"
```

---

## Task 4: `screen` interface + `AppModel` + `progressModel` rename

**Files:**
- Create: `internal/tui/app.go`
- Create: `internal/tui/tui_testhelpers_test.go` ← shared helpers to avoid redeclaration
- Create: `internal/tui/app_test.go`
- Modify: `internal/tui/model.go` (rename `Model`→`progressModel`, `New`→`newProgressModel`; add `screen` methods)
- Modify: `cmd/bootstrap/main.go` (update `runTUI` to use `NewApp`)

This task introduces the screen router pattern. The types `FatalMsg` and `StartStagesMsg`
are exported from the start so `main.go` can `program.Send()` them without needing an
internal-to-tui sender. `InitialMode` and its constants are also exported.

**Critical implementation note for `progressModel.Update`:** The `Update` method must use
a pointer receiver (`*progressModel`) and must `return m, ...` (the same pointer), never a
copy. The `screen.update` wrapper asserts `next.(*progressModel)` after calling `Update`. If
`Update` ever returns a copy, this assertion panics at runtime. The rule: never assign
`newM := *m` and return `&newM` — always mutate `m` in-place and `return m`.

- [ ] **Step 1: Create shared test helpers to avoid redeclaration across test files**

Both `app_test.go` and `wizard_test.go` need `emptyRC()` and `completeRC()` helpers.
Create a shared file in `package tui` (accessible to all `_test.go` files in the package):

```go
// internal/tui/tui_testhelpers_test.go
package tui

import "github.com/homelab/bootstrap-home-infra/internal/config"

func emptyRC() *config.RunConfig {
    return &config.RunConfig{Config: &config.Config{}, Secrets: &config.Secrets{}}
}

func completeRC() *config.RunConfig {
    return &config.RunConfig{
        Config: &config.Config{
            TargetHost:        "192.168.1.10",
            SSHUser:           "ubuntu",
            SSHKeyPath:        "/home/user/.ssh/id_ed25519",
            InternalDomain:    "lab.example.com",
            CloudflareZoneID:  "abc123",
            BootstrapServerIP: "192.168.1.10",
            LabVLANSubnet:     "192.168.1.0/24",
            AdminEmail:        "admin@example.com",
            VaultShares:       5,
            VaultThreshold:    3,
        },
        Secrets: &config.Secrets{
            CloudflareAPIToken: "cf-token",
            AdminPassword:      "secret",
        },
    }
}
```

- [ ] **Step 2: Write failing tests for `AppModel`**

```go
// internal/tui/app_test.go
package tui

import (
    "errors"
    "testing"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/homelab/bootstrap-home-infra/internal/config"
)

func TestAppModel_WizardToProgress(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    app := NewApp(InitialModeWizard, emptyRC(), resultCh)
    app2, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
    appM := app2.(AppModel)

    // Initial screen should be wizardModel.
    if _, ok := appM.current.(*wizardModel); !ok {
        t.Fatalf("expected wizardModel as initial screen, got %T", appM.current)
    }

    // Simulate main.go goroutine sending StartStagesMsg.
    app3, _ := appM.Update(StartStagesMsg{RC: completeRC()})
    appM3 := app3.(AppModel)

    if _, ok := appM3.current.(*progressModel); !ok {
        t.Fatalf("expected progressModel after StartStagesMsg, got %T", appM3.current)
    }
}

func TestAppModel_FatalMsg(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    app := NewApp(InitialModeWizard, emptyRC(), resultCh)
    app, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
    appM := app.(AppModel)

    _, cmd := appM.Update(FatalMsg{Err: errors.New("disk write failed")})
    if cmd == nil {
        t.Fatal("expected non-nil tea.Cmd for FatalMsg")
    }
    msg := cmd()
    if _, ok := msg.(tea.QuitMsg); !ok {
        t.Errorf("expected tea.QuitMsg from FatalMsg cmd, got %T", msg)
    }
}

func TestAppModel_ScreenSwapCallsInit(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    app := NewApp(InitialModeWizard, emptyRC(), resultCh)
    app, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
    appM := app.(AppModel)

    before := appM.current
    app2, _ := appM.Update(StartStagesMsg{RC: completeRC()})
    appM2 := app2.(AppModel)

    if appM2.current == before {
        t.Error("screen did not swap after StartStagesMsg")
    }
    // Verify no panic — init() on the new progressModel is called by AppModel.
}
```

- [ ] **Step 3: Run tests to confirm they fail**

```
go test ./internal/tui/... -run 'TestAppModel'
```
Expected: compile errors (AppModel, InitialModeWizard, NewApp, FatalMsg, StartStagesMsg not defined).

- [ ] **Step 4: Rename `Model` → `progressModel` and `New` → `newProgressModel` in `model.go`**

Make these changes to `model.go`:

1. Change `type Model struct` → `type progressModel struct`
2. Change `func New(stages []StageStatus, logPath string) Model` → `func newProgressModel(stages []StageStatus, logPath string) *progressModel`; change `return Model{...}` → `return &progressModel{...}`
3. Change all method receivers from `(m Model)` to `(m *progressModel)`
4. **Critical:** In `Update`, change the return to always `return m, tea.Batch(cmds...)` where `m` is the *same pointer* receiver. Never create a copy: instead of `return m, ...` after assigning to a local copy, assign directly to the receiver's fields (`m.field = value`). This guarantees `screen.update`'s type assertion `next.(*progressModel)` always succeeds.
5. Add screen interface methods after the existing `Init`/`Update`/`View`:

```go
// screen interface implementation.

func (m *progressModel) init() tea.Cmd { return nil }

func (m *progressModel) update(msg tea.Msg) (screen, tea.Cmd) {
    // Update mutates m in-place and returns m (same pointer).
    _, cmd := m.Update(msg)
    return m, cmd
}

func (m *progressModel) view() string { return m.View() }

func (m *progressModel) setSize(w, h int) {
    m.width = w
    m.height = h
    logWidth := (w * 70 / 100) - 4
    if logWidth < 20 {
        logWidth = 20
    }
    logHeight := h - 6
    if logHeight < 5 {
        logHeight = 5
    }
    m.logView.Width = logWidth
    m.logView.Height = logHeight
}
```

Note: `progressModel.update` calls `m.Update(msg)` which returns `(tea.Model, tea.Cmd)`.
Since `Update` uses a pointer receiver and always returns `m` (the same pointer), the
return value of `m.Update(msg)` is `(m, cmd)`. We only need `cmd`; we discard the
`tea.Model` return because we already have `m`. There is no type assertion needed.

6. `newProgressModel` must handle nil or empty stages gracefully:

```go
func newProgressModel(stages []StageStatus, logPath string) *progressModel {
    if stages == nil {
        stages = []StageStatus{}
    }
    vp := viewport.New(80, 20)
    ti := textinput.New()
    ti.CharLimit = 512
    return &progressModel{
        stages:  stages,
        logView: vp,
        input:   ti,
        mode:    "progress",
        logPath: logPath,
    }
}
```

- [ ] **Step 5: Create `app.go`**

`app.go` must NOT import `internal/stage` — only `internal/config` and bubbletea packages.
`[]StageStatus` and `logPath` are stored in `AppModel` at construction and used when
constructing `progressModel` on `StartStagesMsg`, so the progress screen always shows the
correct stage list.

```go
// internal/tui/app.go
package tui

import (
    tea "github.com/charmbracelet/bubbletea"
    "github.com/homelab/bootstrap-home-infra/internal/config"
)

// screen is the internal interface for TUI screens.
type screen interface {
    init() tea.Cmd
    update(tea.Msg) (screen, tea.Cmd)
    view() string
    setSize(width, height int)
}

// InitialMode controls which screen AppModel starts with.
type InitialMode int

const (
    InitialModeWizard   InitialMode = iota // no config / --wizard flag
    InitialModePreamble                    // config present, show "use it?" prompt
)

// StartStagesMsg is sent by the main.go goroutine after wizard completion.
// AppModel swaps to progressModel on receiving it.
type StartStagesMsg struct {
    RC *config.RunConfig
}

// FatalMsg is sent when wizard post-processing fails.
// AppModel renders the error and quits.
type FatalMsg struct {
    Err error
}

// AppModel is the root tea.Model. Holds an active screen and delegates to it.
type AppModel struct {
    current  screen
    width    int
    height   int
    resultCh chan config.WizardResult
    stages   []StageStatus // stored for progressModel construction
    logPath  string        // stored for progressModel construction
    errMsg   string
}

// NewApp constructs the root AppModel.
//   - mode:     determines initial screen
//   - rc:       preloaded RunConfig (may be incomplete in wizard path)
//   - resultCh: buffered channel (size 1) for WizardResult from wizardModel
//   - stages:   stage statuses for the progress screen
//   - logPath:  log file path for the progress screen
func NewApp(mode InitialMode, rc *config.RunConfig, resultCh chan config.WizardResult, stages []StageStatus, logPath string) AppModel {
    var initial screen
    switch mode {
    case InitialModePreamble:
        initial = newWizardModel(InitialModePreamble, rc, resultCh)
    default:
        initial = newWizardModel(InitialModeWizard, rc, resultCh)
    }
    return AppModel{
        current:  initial,
        resultCh: resultCh,
        stages:   stages,
        logPath:  logPath,
    }
}

// Init satisfies tea.Model.
func (a AppModel) Init() tea.Cmd {
    return a.current.init()
}

// Update satisfies tea.Model.
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        a.width = msg.Width
        a.height = msg.Height
        a.current.setSize(msg.Width, msg.Height)

    case FatalMsg:
        a.errMsg = msg.Err.Error()
        return a, tea.Quit

    case StartStagesMsg:
        pm := newProgressModel(a.stages, a.logPath)
        pm.setSize(a.width, a.height)
        a.current = pm
        return a, pm.init()
    }

    // Delegate to current screen.
    next, cmd := a.current.update(msg)
    if next != a.current {
        // Screen swap detected: call init() on new screen and batch cmds.
        a.current = next
        next.setSize(a.width, a.height)
        initCmd := next.init()
        return a, tea.Batch(cmd, initCmd)
    }
    return a, cmd
}

// View satisfies tea.Model.
func (a AppModel) View() string {
    if a.errMsg != "" {
        return styleHeader.Width(a.width).Render("bootstrap-home-infra") + "\n" +
            styleErrorPanel.Width(a.width-4).Render("FATAL: "+a.errMsg)
    }
    return a.current.view()
}
```

- [ ] **Step 6: Update `main.go` to use `NewApp` (temporary wiring — full wiring in Task 7)**

Update `runTUI` signature and body to use `NewApp`. Pass the statuses and logPath that
were already computed. Use `InitialModePreamble` as a placeholder — Task 7 replaces this
with `selectInitialScreen`:

```go
func runTUI(rc *config.RunConfig, allStages []stage.Stage, targetStage string, runDir string) error {
    statuses := make([]tui.StageStatus, len(allStages))
    for i, s := range allStages {
        statuses[i] = tui.StageStatus{Name: s.Name(), State: "pending"}
    }
    logPath := fmt.Sprintf("logs/%s/bootstrap.log", runDir)

    resultCh := make(chan config.WizardResult, 1)
    appModel := tui.NewApp(tui.InitialModePreamble, rc, resultCh, statuses, logPath)
    program := tea.NewProgram(appModel, tea.WithAltScreen())
    tuiSink := tui.NewTUISink(program)

    runErrChan := make(chan error, 1)
    go func() {
        ctx := context.Background()
        runner := stage.NewRunner(allStages, tuiSink)
        var err error
        if targetStage != "" {
            err = runner.RunStage(ctx, rc, targetStage)
        } else {
            err = runner.Run(ctx, rc)
        }
        runErrChan <- err
        tuiSink.Quit()
    }()

    finalModel, err := program.Run()
    if err != nil {
        return fmt.Errorf("TUI error: %w", err)
    }
    _ = finalModel
    return <-runErrChan
}
```

Also remove the `_ = finalModel` + `runErr := <-runErrChan; return runErr` pattern that
existed in the original and replace with the cleaner version above.

- [ ] **Step 7: Verify the codebase compiles**

```
go build ./...
```
Expected: no errors.

- [ ] **Step 8: Run all tests**

```
go test ./...
```
Expected: AppModel tests pass; all pre-existing tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/tui/app.go internal/tui/tui_testhelpers_test.go internal/tui/app_test.go internal/tui/model.go cmd/bootstrap/main.go
git commit -m "feat(tui): add AppModel screen router; rename Model→progressModel"
```

---

## Task 5: SSH key detection + `wizardModel` (preamble + field navigation)

**Files:**
- Create: `internal/tui/sshkey.go`
- Create: `internal/tui/sshkey_test.go`
- Create: `internal/tui/wizard.go`
- Create: `internal/tui/wizard_test.go`

- [ ] **Step 1: Write failing tests for SSH key detection**

```go
// internal/tui/sshkey_test.go
package tui

import (
    "os"
    "path/filepath"
    "testing"
)

func TestDetectSSHKey_Ed25519Found(t *testing.T) {
    dir := t.TempDir()
    keyPath := filepath.Join(dir, "id_ed25519")
    if err := os.WriteFile(keyPath, []byte("fake key"), 0600); err != nil {
        t.Fatal(err)
    }
    if got := detectSSHKey(dir); got != keyPath {
        t.Errorf("detectSSHKey = %q, want %q", got, keyPath)
    }
}

func TestDetectSSHKey_RsaFallback(t *testing.T) {
    dir := t.TempDir()
    rsaPath := filepath.Join(dir, "id_rsa")
    if err := os.WriteFile(rsaPath, []byte("fake rsa"), 0600); err != nil {
        t.Fatal(err)
    }
    if got := detectSSHKey(dir); got != rsaPath {
        t.Errorf("detectSSHKey = %q, want %q", got, rsaPath)
    }
}

func TestDetectSSHKey_NoneFound(t *testing.T) {
    dir := t.TempDir()
    if got := detectSSHKey(dir); got != "" {
        t.Errorf("detectSSHKey = %q, want empty", got)
    }
}
```

- [ ] **Step 2: Write failing tests for `wizardModel`**

```go
// internal/tui/wizard_test.go
package tui

import (
    "strings"
    "testing"

    "github.com/charmbracelet/bubbles/textinput"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/homelab/bootstrap-home-infra/internal/config"
)

// helpers

func emptyPreload() *config.RunConfig {
    return &config.RunConfig{Config: &config.Config{}, Secrets: &config.Secrets{}}
}

func completePreload() *config.RunConfig { return completeRC() }

func pressEnter() tea.Msg { return tea.KeyMsg{Type: tea.KeyEnter} }
func pressEsc() tea.Msg   { return tea.KeyMsg{Type: tea.KeyEsc} }
func typeKey(s string) tea.Msg {
    return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// advanceTo returns a wizardModel at field index idx (0-based) with all prior
// fields filled with valid placeholder values. Uses a bare IPv4 for field 0,
// which causes field 5 (bootstrap_server_ip) to be skipped automatically.
// Field indices after the skip: 6=lab_vlan_subnet, 7=admin_email,
// 8=CF_DNS_API_TOKEN, 9=BOOTSTRAP_ADMIN_PASSWORD.
func advanceTo(idx int, resultCh chan config.WizardResult) *wizardModel {
    m := newWizardModel(InitialModeWizard, emptyPreload(), resultCh)
    m.setSize(80, 24)

    // One valid value per displayed field (field 5 is skipped for bare IPv4).
    // indices 0..8 correspond to fields 0,1,2,3,4, 6,7,8,9 (5 is skipped).
    validValues := []string{
        "192.168.1.100",  // 0: target_host (bare IP → skip field 5)
        "ubuntu",         // 1: ssh_user
        "/tmp/id_test",   // 2: ssh_key_path
        "home.lab",       // 3: internal_domain
        "zoneabc123",     // 4: cloudflare_zone_id
        "10.0.0.0/24",    // 5 (display): lab_vlan_subnet  (field index 6)
        "admin@test.com", // 6 (display): admin_email      (field index 7)
        "tok-abc",        // 7 (display): CF_DNS_API_TOKEN  (field index 8)
        "pw-placeholder", // 8 (display): first BOOTSTRAP_ADMIN_PASSWORD entry (field index 9)
    }

    for i := 0; i < idx && i < len(validValues); i++ {
        m.input.SetValue(validValues[i])
        next, _ := m.update(pressEnter())
        m = next.(*wizardModel)
    }
    return m
}

// ── Tests ──────────────────────────────────────────────────────────────────

func TestWizardModel_FieldAdvance(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := newWizardModel(InitialModeWizard, emptyPreload(), resultCh)
    m.setSize(80, 24)

    m.input.SetValue("192.168.1.100")
    next, _ := m.update(pressEnter())
    m2 := next.(*wizardModel)

    if m2.cursor != 1 {
        t.Errorf("cursor = %d, want 1", m2.cursor)
    }
    if m2.values["target_host"] != "192.168.1.100" {
        t.Errorf("values[target_host] = %q", m2.values["target_host"])
    }
}

func TestWizardModel_BackNavigation(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := advanceTo(2, resultCh) // at ssh_key_path (cursor=2)
    if m.cursor != 2 {
        t.Fatalf("setup: cursor = %d, want 2", m.cursor)
    }
    next, _ := m.update(pressEsc())
    m2 := next.(*wizardModel)
    if m2.cursor != 1 {
        t.Errorf("after esc: cursor = %d, want 1", m2.cursor)
    }
    if m2.values["ssh_user"] == "" {
        t.Error("ssh_user lost after back navigation")
    }
}

func TestWizardModel_BackNavigation_NoopOnFirst(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := newWizardModel(InitialModeWizard, emptyPreload(), resultCh)
    m.setSize(80, 24)
    next, _ := m.update(pressEsc())
    m2 := next.(*wizardModel)
    if m2.cursor != 0 {
        t.Errorf("esc on first field: cursor = %d, want 0", m2.cursor)
    }
}

func TestWizardModel_ValidationError(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := newWizardModel(InitialModeWizard, emptyPreload(), resultCh)
    m.setSize(80, 24)
    m.input.SetValue("") // empty — fails validateNonEmpty
    next, _ := m.update(pressEnter())
    m2 := next.(*wizardModel)
    if m2.cursor != 0 {
        t.Errorf("cursor advanced past invalid input: %d", m2.cursor)
    }
    if m2.errMsg == "" {
        t.Error("errMsg empty after validation failure")
    }
}

func TestWizardModel_PasswordMasked(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    // CF_DNS_API_TOKEN is field index 8 (display index 7 with skip).
    // advanceTo(8) advances through 8 fields, leaving cursor at field index 8.
    m := advanceTo(8, resultCh)
    if m.fields[m.cursor].key != "CF_DNS_API_TOKEN" {
        t.Fatalf("expected CF_DNS_API_TOKEN at cursor %d, got %q", m.cursor, m.fields[m.cursor].key)
    }
    if m.input.EchoMode != textinput.EchoPassword {
        t.Errorf("EchoMode = %v, want EchoPassword", m.input.EchoMode)
    }
}

func TestWizardModel_PreambleAccept(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := newWizardModel(InitialModePreamble, completePreload(), resultCh)
    m.setSize(80, 24)
    if m.state != "preamble" {
        t.Fatalf("state = %q, want preamble", m.state)
    }
    _, cmd := m.update(pressEnter())
    if cmd == nil {
        t.Fatal("cmd nil after preamble accept")
    }
    cmd() // sends on resultCh
    select {
    case result := <-resultCh:
        if !result.Noop {
            t.Errorf("expected Noop=true, got %+v", result)
        }
    default:
        t.Error("WizardResult not sent on resultCh after preamble accept")
    }
}

func TestWizardModel_PreambleReject(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := newWizardModel(InitialModePreamble, completePreload(), resultCh)
    m.setSize(80, 24)
    next, _ := m.update(typeKey("n"))
    m2 := next.(*wizardModel)
    if m2.state != "fields" {
        t.Errorf("state = %q, want fields", m2.state)
    }
    if m2.cursor != 0 {
        t.Errorf("cursor = %d, want 0", m2.cursor)
    }
    if m2.values["target_host"] != "192.168.1.10" {
        t.Errorf("target_host not pre-filled: %q", m2.values["target_host"])
    }
}

func TestWizardModel_PasswordConfirmMismatch(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    // Field 9 = BOOTSTRAP_ADMIN_PASSWORD. advanceTo(9) advances through 9 fields.
    m := advanceTo(9, resultCh)
    if m.fields[m.cursor].key != "BOOTSTRAP_ADMIN_PASSWORD" {
        t.Fatalf("expected BOOTSTRAP_ADMIN_PASSWORD at cursor %d, got %q", m.cursor, m.fields[m.cursor].key)
    }

    // Submit first password entry.
    m.input.SetValue("password1")
    next, _ := m.update(pressEnter())
    m2 := next.(*wizardModel)
    if !m2.confirmMode {
        t.Fatal("expected confirmMode=true after first password entry")
    }

    // Submit mismatched confirm.
    m2.input.SetValue("password2")
    next2, _ := m2.update(pressEnter())
    m3 := next2.(*wizardModel)

    if m3.errMsg == "" {
        t.Error("expected errMsg after password mismatch")
    }
    if !strings.Contains(m3.errMsg, "do not match") {
        t.Errorf("errMsg = %q, want to contain 'do not match'", m3.errMsg)
    }
    if m3.input.Value() != "" {
        t.Errorf("input not cleared after mismatch: %q", m3.input.Value())
    }
    if m3.confirmMode {
        t.Error("confirmMode still true after mismatch")
    }
    // Cursor should not advance.
    if m3.cursor != m.cursor {
        t.Errorf("cursor advanced from %d to %d after mismatch", m.cursor, m3.cursor)
    }
}

func TestWizardModel_CompletionEmitsResult(t *testing.T) {
    resultCh := make(chan config.WizardResult, 1)
    m := advanceTo(9, resultCh) // at BOOTSTRAP_ADMIN_PASSWORD

    // First entry.
    m.input.SetValue("securepass")
    next, _ := m.update(pressEnter())
    m2 := next.(*wizardModel)
    if !m2.confirmMode {
        t.Fatal("expected confirmMode after first password entry")
    }

    // Matching confirm.
    m2.input.SetValue("securepass")
    _, cmd := m2.update(pressEnter())
    if cmd == nil {
        t.Fatal("cmd nil after wizard completion")
    }
    cmd()

    select {
    case result := <-resultCh:
        if result.Noop {
            t.Error("Noop should be false for wizard completion")
        }
        if result.Secrets["BOOTSTRAP_ADMIN_PASSWORD"] != "securepass" {
            t.Errorf("BOOTSTRAP_ADMIN_PASSWORD = %q, want securepass",
                result.Secrets["BOOTSTRAP_ADMIN_PASSWORD"])
        }
        if result.Values["target_host"] == "" {
            t.Error("target_host empty in WizardResult.Values")
        }
    default:
        t.Error("WizardResult not sent on resultCh")
    }
}
```

- [ ] **Step 3: Run tests to confirm they fail**

```
go test ./internal/tui/... -run 'TestDetectSSHKey|TestWizardModel'
```
Expected: compile errors.

- [ ] **Step 4: Implement `sshkey.go`**

```go
// internal/tui/sshkey.go
package tui

import (
    "os"
    "path/filepath"
)

// detectSSHKey returns the first found SSH private key in sshDir, checking
// id_ed25519, id_rsa, id_ed25519_sk in order. Returns "" if none found.
func detectSSHKey(sshDir string) string {
    for _, name := range []string{"id_ed25519", "id_rsa", "id_ed25519_sk"} {
        p := filepath.Join(sshDir, name)
        if _, err := os.Stat(p); err == nil {
            return p
        }
    }
    return ""
}
```

- [ ] **Step 5: Implement `wizard.go`**

```go
// internal/tui/wizard.go
package tui

import (
    "fmt"
    "net"
    "os"
    "path/filepath"
    "strings"

    "github.com/charmbracelet/bubbles/textinput"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    "github.com/homelab/bootstrap-home-infra/internal/config"
)

type fieldDef struct {
    key         string
    label       string
    description string
    example     string
    isSecret    bool
    validate    func(string) error
}

// wizardModel collects configuration interactively, one field at a time.
type wizardModel struct {
    state   string // "preamble" | "fields"
    cursor  int
    fields  []fieldDef
    values  map[string]string
    secrets map[string]string
    input   textinput.Model
    errMsg  string
    width   int
    height  int

    confirmMode  bool
    confirmFirst string

    skipBootstrapIP bool

    resultCh chan config.WizardResult
    preload  *config.RunConfig
}

// bootstrapIPFieldIdx is the index of the bootstrap_server_ip field in the fields slice.
const bootstrapIPFieldIdx = 5

func newWizardModel(mode InitialMode, rc *config.RunConfig, resultCh chan config.WizardResult) *wizardModel {
    ti := textinput.New()
    ti.CharLimit = 512
    ti.Focus()

    homeDir, _ := os.UserHomeDir()
    detectedKey := detectSSHKey(filepath.Join(homeDir, ".ssh"))

    fields := []fieldDef{
        {key: "target_host", label: "Target host",
            description: "IP address or hostname of the server to bootstrap.",
            example:     "192.168.1.100", validate: validateNonEmpty},
        {key: "ssh_user", label: "SSH user",
            description: "Username for SSH access (must have passwordless sudo).",
            example:     "ubuntu", validate: validateNonEmpty},
        {key: "ssh_key_path", label: "SSH private key path",
            description: "Path to the private key used to reach your server.",
            example:     detectedKey, validate: validateNonEmpty},
        {key: "internal_domain", label: "Internal domain",
            description: "Private domain for internal services (DNS, TLS).",
            example:     "home.lab", validate: validateNonEmpty},
        {key: "cloudflare_zone_id", label: "Cloudflare Zone ID",
            description: "Found at cloudflare.com → your domain → Overview → Zone ID.",
            example:     "a1b2c3d4...", validate: validateNonEmpty},
        {key: "bootstrap_server_ip", label: "Bootstrap server IP",
            description: "IP address of this machine (used in Ansible inventory).",
            example:     "192.168.1.100", validate: validateIP},
        {key: "lab_vlan_subnet", label: "Lab VLAN subnet",
            description: "CIDR subnet for your lab network. Advanced options (VLAN ID, Vault shares) can be edited by re-running with --wizard after setup.",
            example:     "10.0.0.0/24", validate: validateCIDR},
        {key: "admin_email", label: "Admin email",
            description: "Used for TLS certificate notifications and admin account creation.",
            example:     "admin@example.com", validate: validateNonEmpty},
        {key: "CF_DNS_API_TOKEN", label: "Cloudflare API token",
            description: "DNS-01 challenge token (needs Zone:DNS:Edit permission).",
            isSecret: true, validate: validateNonEmpty},
        {key: "BOOTSTRAP_ADMIN_PASSWORD", label: "Admin password",
            description: "Password for the bootstrapped admin account.",
            isSecret: true, validate: validateNonEmpty},
    }

    m := &wizardModel{
        state:    "fields",
        fields:   fields,
        values:   make(map[string]string),
        secrets:  make(map[string]string),
        input:    ti,
        resultCh: resultCh,
        preload:  rc,
    }

    if mode == InitialModePreamble {
        m.state = "preamble"
    }

    if rc != nil && rc.Config != nil {
        prefillWizard(m, rc)
    }
    if detectedKey != "" && m.values["ssh_key_path"] == "" {
        m.values["ssh_key_path"] = detectedKey
    }

    m.applyFieldToInput()
    return m
}

func prefillWizard(m *wizardModel, rc *config.RunConfig) {
    set := func(k, v string) {
        if v != "" {
            m.values[k] = v
        }
    }
    set("target_host", rc.TargetHost)
    set("ssh_user", rc.SSHUser)
    set("ssh_key_path", rc.SSHKeyPath)
    set("internal_domain", rc.InternalDomain)
    set("cloudflare_zone_id", rc.CloudflareZoneID)
    set("bootstrap_server_ip", rc.BootstrapServerIP)
    set("lab_vlan_subnet", rc.LabVLANSubnet)
    set("admin_email", rc.AdminEmail)
    if rc.Secrets != nil {
        if rc.Secrets.CloudflareAPIToken != "" {
            m.secrets["CF_DNS_API_TOKEN"] = rc.Secrets.CloudflareAPIToken
        }
        if rc.Secrets.AdminPassword != "" {
            m.secrets["BOOTSTRAP_ADMIN_PASSWORD"] = rc.Secrets.AdminPassword
        }
    }
}

func (m *wizardModel) applyFieldToInput() {
    if m.cursor >= len(m.fields) {
        return
    }
    f := m.fields[m.cursor]
    m.input.Reset()
    m.errMsg = ""
    m.input.EchoMode = textinput.EchoNormal
    if f.isSecret {
        m.input.EchoMode = textinput.EchoPassword
        if v := m.secrets[f.key]; v != "" {
            m.input.SetValue(v)
        }
    } else {
        if v := m.values[f.key]; v != "" {
            m.input.SetValue(v)
        }
    }
}

func (m *wizardModel) totalFields() int {
    if m.skipBootstrapIP {
        return len(m.fields) - 1
    }
    return len(m.fields)
}

func (m *wizardModel) visibleCursor() int {
    if m.skipBootstrapIP && m.cursor > bootstrapIPFieldIdx {
        return m.cursor // bootstrapIPFieldIdx was skipped; display matches cursor
    }
    return m.cursor + 1
}

func (m *wizardModel) init() tea.Cmd { return textinput.Blink }

func (m *wizardModel) update(msg tea.Msg) (screen, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if m.state == "preamble" {
            return m.handlePreamble(msg)
        }
        return m.handleFields(msg)
    }
    var cmd tea.Cmd
    m.input, cmd = m.input.Update(msg)
    return m, cmd
}

func (m *wizardModel) handlePreamble(msg tea.KeyMsg) (screen, tea.Cmd) {
    switch msg.Type {
    case tea.KeyEnter:
        return m.sendNoop()
    case tea.KeyCtrlC:
        return m, tea.Quit
    }
    switch msg.String() {
    case "y", "Y":
        return m.sendNoop()
    case "n", "N":
        m.state = "fields"
        m.cursor = 0
        m.applyFieldToInput()
    }
    return m, nil
}

func (m *wizardModel) sendNoop() (screen, tea.Cmd) {
    ch := m.resultCh
    return m, func() tea.Msg {
        ch <- config.WizardResult{Noop: true}
        return nil
    }
}

func (m *wizardModel) handleFields(msg tea.KeyMsg) (screen, tea.Cmd) {
    switch msg.Type {
    case tea.KeyCtrlC:
        return m, tea.Quit
    case tea.KeyEsc:
        if m.cursor > 0 {
            m.cursor--
            if m.skipBootstrapIP && m.cursor == bootstrapIPFieldIdx {
                m.cursor--
            }
            m.applyFieldToInput()
        }
        return m, nil
    case tea.KeyEnter:
        return m.handleEnter()
    }
    var cmd tea.Cmd
    m.input, cmd = m.input.Update(msg)
    return m, cmd
}

func (m *wizardModel) handleEnter() (screen, tea.Cmd) {
    f := m.fields[m.cursor]
    val := strings.TrimSpace(m.input.Value())

    // Password confirm sub-step.
    if m.confirmMode {
        if val != m.confirmFirst {
            m.errMsg = "Passwords do not match — please try again."
            m.input.Reset()
            m.confirmFirst = ""
            m.confirmMode = false
            return m, nil
        }
        m.secrets[f.key] = m.confirmFirst
        m.confirmFirst = ""
        m.confirmMode = false
        return m.advance()
    }

    if f.validate != nil {
        if err := f.validate(val); err != nil {
            m.errMsg = err.Error()
            return m, nil
        }
    }

    if f.isSecret {
        if f.key == "BOOTSTRAP_ADMIN_PASSWORD" {
            m.confirmFirst = val
            m.confirmMode = true
            m.input.Reset()
            m.errMsg = ""
            return m, nil
        }
        m.secrets[f.key] = val
    } else {
        m.values[f.key] = val
        if m.cursor == 0 {
            ip, skip := config.DeriveBootstrapServerIP(val)
            m.skipBootstrapIP = skip
            if ip != "" {
                m.values["bootstrap_server_ip"] = ip
            }
        }
    }
    return m.advance()
}

func (m *wizardModel) advance() (screen, tea.Cmd) {
    next := m.cursor + 1
    if m.skipBootstrapIP && next == bootstrapIPFieldIdx {
        next++
    }
    m.cursor = next

    if m.cursor >= len(m.fields) {
        result := config.WizardResult{
            Values:  make(map[string]string),
            Secrets: make(map[string]string),
        }
        for k, v := range m.values {
            result.Values[k] = v
        }
        for k, v := range m.secrets {
            result.Secrets[k] = v
        }
        ch := m.resultCh
        return m, func() tea.Msg {
            ch <- result
            return nil
        }
    }
    m.applyFieldToInput()
    return m, nil
}

func (m *wizardModel) view() string {
    if m.state == "preamble" {
        return m.viewPreamble()
    }
    return m.viewFields()
}

func (m *wizardModel) viewPreamble() string {
    header := styleHeader.Width(m.width).Render("bootstrap-home-infra")
    var sb strings.Builder
    sb.WriteString("\n  Found config.yaml\n\n")
    if m.preload != nil && m.preload.Config != nil {
        c := m.preload.Config
        sb.WriteString(fmt.Sprintf("  target_host:     %s\n", c.TargetHost))
        sb.WriteString(fmt.Sprintf("  ssh_user:        %s\n", c.SSHUser))
        sb.WriteString(fmt.Sprintf("  internal_domain: %s\n", c.InternalDomain))
        sb.WriteString(fmt.Sprintf("  admin_email:     %s\n", c.AdminEmail))
        sb.WriteString("  ...\n")
    }
    sb.WriteString("\n  Use this config and proceed?  [Y/n]\n")
    return header + sb.String()
}

func (m *wizardModel) viewFields() string {
    if m.cursor >= len(m.fields) {
        return styleHeader.Width(m.width).Render("bootstrap-home-infra") + "\n\n  Setup complete."
    }
    header := styleHeader.Width(m.width).Render("bootstrap-home-infra")
    f := m.fields[m.cursor]
    counter := fmt.Sprintf("[%d of %d]", m.visibleCursor(), m.totalFields())
    labelLine := lipgloss.JoinHorizontal(lipgloss.Top,
        stylePromptLabel.Render("  "+f.label),
        lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "+counter),
    )
    desc := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  " + f.description)

    lines := []string{header, "", labelLine, desc}
    if !f.isSecret && f.example != "" && !strings.Contains(f.example, "...") {
        lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
            Render(fmt.Sprintf("  Example: %s", f.example)))
    }
    lines = append(lines, "")
    if m.confirmMode {
        lines = append(lines, "  Confirm password:")
    }
    lines = append(lines, "  "+m.input.View())
    if m.errMsg != "" {
        lines = append(lines, "  "+lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render(m.errMsg))
    }
    lines = append(lines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
        Render("  ↵ accept · esc go back"))
    return strings.Join(lines, "\n")
}

func (m *wizardModel) setSize(w, h int) {
    m.width = w
    m.height = h
}

// ── Validators ───────────────────────────────────────────────────────────────

func validateNonEmpty(s string) error {
    if strings.TrimSpace(s) == "" {
        return fmt.Errorf("this field is required")
    }
    return nil
}

func validateIP(s string) error {
    if strings.TrimSpace(s) == "" {
        return fmt.Errorf("this field is required")
    }
    if net.ParseIP(strings.TrimSpace(s)) == nil {
        return fmt.Errorf("%q is not a valid IP address", s)
    }
    return nil
}

func validateCIDR(s string) error {
    if strings.TrimSpace(s) == "" {
        return fmt.Errorf("this field is required")
    }
    if _, _, err := net.ParseCIDR(strings.TrimSpace(s)); err != nil {
        return fmt.Errorf("%q is not a valid CIDR (e.g. 10.0.0.0/24)", s)
    }
    return nil
}
```

- [ ] **Step 6: Run tests to confirm they pass**

```
go test ./internal/tui/... -run 'TestDetectSSHKey|TestWizardModel'
```
Expected: PASS.

- [ ] **Step 7: Run all tests**

```
go test ./...
```
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/sshkey.go internal/tui/sshkey_test.go internal/tui/wizard.go internal/tui/wizard_test.go
git commit -m "feat(tui): add wizardModel with field navigation, preamble, SSH key detection, and password confirm"
```

---

## Task 6: `main.go` full wiring — `--wizard` flag, `selectInitialScreen`, `resultCh` goroutine

**Files:**
- Modify: `cmd/bootstrap/main.go`
- Create: `cmd/bootstrap/main_test.go`

This task replaces the temporary `InitialModePreamble` stub in `runTUI` with the proper
`selectInitialScreen` logic. The goroutine started in `runTUI` properly handles both the
`Noop` path (existing config accepted) and the wizard-collected path (write files, then
start runner). Stage runner errors are surfaced through a separate error channel so the
binary exits with a non-zero code on stage failure.

- [ ] **Step 1: Write failing test for `selectInitialScreen`**

```go
// cmd/bootstrap/main_test.go
package main

import (
    "testing"

    "github.com/homelab/bootstrap-home-infra/internal/config"
    "github.com/homelab/bootstrap-home-infra/internal/tui"
)

func emptyRCm() *config.RunConfig {
    return &config.RunConfig{Config: &config.Config{}, Secrets: &config.Secrets{}}
}

func completeRCm() *config.RunConfig {
    return &config.RunConfig{
        Config: &config.Config{
            TargetHost:        "192.168.1.10",
            SSHUser:           "ubuntu",
            SSHKeyPath:        "/home/user/.ssh/id_ed25519",
            InternalDomain:    "lab.example.com",
            CloudflareZoneID:  "abc123",
            BootstrapServerIP: "192.168.1.10",
            LabVLANSubnet:     "192.168.1.0/24",
            AdminEmail:        "admin@example.com",
            VaultShares:       5,
            VaultThreshold:    3,
        },
        Secrets: &config.Secrets{
            CloudflareAPIToken: "cf-token",
            AdminPassword:      "secret",
        },
    }
}

func partialRCm() *config.RunConfig {
    rc := emptyRCm()
    rc.TargetHost = "192.168.1.10"
    return rc
}

func TestSelectInitialScreen(t *testing.T) {
    tests := []struct {
        name       string
        rc         *config.RunConfig
        wizardFlag bool
        want       tui.InitialMode
    }{
        {"no config, no flag",     emptyRCm(),    false, tui.InitialModeWizard},
        {"complete config, no flag", completeRCm(), false, tui.InitialModePreamble},
        {"complete config, wizard", completeRCm(), true,  tui.InitialModeWizard},
        {"partial config, no flag", partialRCm(),  false, tui.InitialModeWizard},
        {"empty config, wizard",    emptyRCm(),    true,  tui.InitialModeWizard},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            got := selectInitialScreen(tc.rc, tc.wizardFlag)
            if got != tc.want {
                t.Errorf("selectInitialScreen() = %v, want %v", got, tc.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run test to confirm it fails**

```
go test ./cmd/bootstrap/... -run TestSelectInitialScreen
```
Expected: compile error (`selectInitialScreen` not defined).

- [ ] **Step 3: Update `main.go` with full wizard wiring**

Add `flagWizard`:
```go
var flagWizard bool
```

In `init()`:
```go
rootCmd.Flags().BoolVar(&flagWizard, "wizard", false,
    "Run setup wizard (pre-fills from existing config if present)")
```

Add `selectInitialScreen`:
```go
// selectInitialScreen determines the initial TUI mode. Pure function; testable
// without starting a TUI.
func selectInitialScreen(rc *config.RunConfig, wizardFlag bool) tui.InitialMode {
    if wizardFlag {
        return tui.InitialModeWizard
    }
    if config.Validate(rc) != nil {
        return tui.InitialModeWizard
    }
    return tui.InitialModePreamble
}
```

Pass `flagWizard` to `runTUI`:
```go
    return runTUI(rc, allStages, flagStage, runDir, flagWizard)
```

Replace `runTUI` with the fully wired version:

```go
func runTUI(rc *config.RunConfig, allStages []stage.Stage, targetStage, runDir string, wizardFlag bool) error {
    statuses := make([]tui.StageStatus, len(allStages))
    for i, s := range allStages {
        statuses[i] = tui.StageStatus{Name: s.Name(), State: "pending"}
    }
    logPath := fmt.Sprintf("logs/%s/bootstrap.log", runDir)

    resultCh := make(chan config.WizardResult, 1)
    mode := selectInitialScreen(rc, wizardFlag)
    appModel := tui.NewApp(mode, rc, resultCh, statuses, logPath)
    program := tea.NewProgram(appModel, tea.WithAltScreen())
    tuiSink := tui.NewTUISink(program)

    // runnerErrCh receives the stage runner's final error.
    runnerErrCh := make(chan error, 1)

    // Goroutine 1: waits for WizardResult → processes → launches stage runner.
    go func() {
        result := <-resultCh
        var activeRC *config.RunConfig
        if result.Noop {
            activeRC = rc
        } else {
            var err error
            activeRC, err = config.FromWizardResult(result)
            if err != nil {
                program.Send(tui.FatalMsg{Err: err})
                runnerErrCh <- err
                return
            }
            if err := config.Validate(activeRC); err != nil {
                program.Send(tui.FatalMsg{Err: err})
                runnerErrCh <- err
                return
            }
            if err := config.WriteConfigYAML(flagConfig, activeRC.Config); err != nil {
                program.Send(tui.FatalMsg{Err: err})
                runnerErrCh <- err
                return
            }
            if err := config.WriteEnvFile(flagEnv, activeRC.Secrets); err != nil {
                program.Send(tui.FatalMsg{Err: err})
                runnerErrCh <- err
                return
            }
        }

        // Notify AppModel to swap to progressModel.
        program.Send(tui.StartStagesMsg{RC: activeRC})

        // Goroutine 2: stage runner.
        go func() {
            ctx := context.Background()
            runner := stage.NewRunner(allStages, tuiSink)
            var err error
            if targetStage != "" {
                err = runner.RunStage(ctx, activeRC, targetStage)
            } else {
                err = runner.Run(ctx, activeRC)
            }
            runnerErrCh <- err
            tuiSink.Quit()
        }()
    }()

    if _, err := program.Run(); err != nil {
        return fmt.Errorf("TUI error: %w", err)
    }

    // Surface stage runner error as the process exit code.
    // Use a non-blocking select: if the user pressed Ctrl+C before wizard
    // completion, runnerErrCh is empty (Goroutine 1 never unblocked, Goroutine 2
    // never started). A blocking read would deadlock. Default = clean exit code 0.
    select {
    case err := <-runnerErrCh:
        return err
    default:
        return nil
    }
}
```

> **Note on `runnerErrCh` drain:** Three exit paths exist after `program.Run()` returns:
> 1. **Stage runner completed** — Goroutine 2 sent on `runnerErrCh` and called `tuiSink.Quit()`. Channel has one value. `select` reads it.
> 2. **Wizard post-processing failed** — Goroutine 1 sent a `FatalMsg` then sent on `runnerErrCh`. `FatalMsg` caused `tea.Quit`. Channel has one value. `select` reads it.
> 3. **Ctrl+C before wizard completes** — `tea.Quit` was called by key handler. Goroutine 1 is still blocked on `resultCh`. Channel is empty. `select default` fires → exit code 0. Goroutine 1 is leaked but will be collected at process exit.
> The `select` with `default` handles all three paths without deadlock.

- [ ] **Step 4: Run the test**

```
go test ./cmd/bootstrap/... -run TestSelectInitialScreen
```
Expected: PASS.

- [ ] **Step 5: Build the full binary**

```
go build ./...
```
Expected: no errors.

- [ ] **Step 6: Run all tests**

```
go test ./...
```
Expected: all pass.

- [ ] **Step 7: Smoke test — all three entry paths**

```bash
# Path 1: no config.yaml → wizard
mv config.yaml config.yaml.bak 2>/dev/null || true
./bin/bootstrap
# Expected: wizard screen appears; Ctrl+C to exit
mv config.yaml.bak config.yaml 2>/dev/null || true

# Path 2: config present, no flag → preamble prompt
./bin/bootstrap
# Expected: "Use this config and proceed? [Y/n]" screen; Ctrl+C to exit

# Path 3: --wizard flag → wizard with pre-filled values
./bin/bootstrap --wizard
# Expected: wizard screen with existing config pre-filled; Ctrl+C to exit
```

- [ ] **Step 8: Commit**

```bash
git add cmd/bootstrap/main.go cmd/bootstrap/main_test.go
git commit -m "feat(main): wire first-run wizard — --wizard flag, selectInitialScreen, resultCh goroutine"
```

---

## Final Verification

- [ ] **Run the full test suite**

```
go test ./...
```
Expected: all pass.

- [ ] **Run linter**

```
go vet ./...
```
Expected: no issues.

- [ ] **Build binary**

```
go build -o bin/bootstrap ./cmd/bootstrap
```
Expected: no errors.

- [ ] **Headless mode regression (must still work with complete config)**

```bash
just write-test-config
just vm-up
just test-stage config-collection
```
Expected: ConfigStage validates, tests SSH, writes config.yaml. Does NOT write `.env`.
Exit code 0.
