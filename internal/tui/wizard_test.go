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

func fieldIndex(m *wizardModel, key string) int {
	for i, field := range m.fields {
		if field.key == key {
			return i
		}
	}
	return -1
}

func validWizardValue(key string) string {
	values := map[string]string{
		"target_host":                  "192.168.1.100",
		"ssh_user":                     "ubuntu",
		"ssh_key_path":                 "/tmp/id_test",
		"ssh_host_fingerprint":         "",
		"internal_domain":              "home.example.com",
		"cloudflare_zone_id":           "zoneabc123",
		"lab_vlan_subnet":              "10.0.0.0/24",
		"lab_gateway":                  "10.0.0.1",
		"admin_email":                  "admin@test.com",
		"bootstrap_server_ip":          "192.168.1.100",
		"vault_init_output_path":       "/tmp/vault-init.json",
		"CF_DNS_API_TOKEN":             "tok-abc",
		"CONTROLLER_RECOVERY_PASSWORD": "controller-recovery-password",
		"BOOTSTRAP_ADMIN_PASSWORD":     "pw-placeholder",
	}
	return values[key]
}

// advanceToField fills every preceding field with a valid value.
func advanceToField(key string, resultCh chan config.WizardResult) *wizardModel {
	m := newWizardModel(InitialModeWizard, emptyPreload(), resultCh)
	m.setSize(80, 24)
	target := fieldIndex(m, key)
	if target < 0 {
		panic("unknown wizard field: " + key)
	}
	for m.cursor < target {
		value := validWizardValue(m.fields[m.cursor].key)
		if value != "" || m.input.Value() == "" {
			m.input.SetValue(value)
		}
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
	m := advanceToField("ssh_key_path", resultCh)
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
	m := advanceToField("CF_DNS_API_TOKEN", resultCh)
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
	m := advanceToField("BOOTSTRAP_ADMIN_PASSWORD", resultCh)
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

func TestWizardModel_BackNavigation_SkipsBootstrapIP(t *testing.T) {
	resultCh := make(chan config.WizardResult, 1)
	m := advanceToField("CF_DNS_API_TOKEN", resultCh)
	if m.fields[m.cursor].key != "CF_DNS_API_TOKEN" {
		t.Fatalf("setup: cursor %d = %q, want CF_DNS_API_TOKEN", m.cursor, m.fields[m.cursor].key)
	}
	next, _ := m.update(pressEsc())
	m2 := next.(*wizardModel)
	// Should land on the Vault recovery path, with the derived bootstrap IP
	// already skipped earlier in the flow.
	want := fieldIndex(m2, "vault_init_output_path")
	if m2.cursor != want {
		t.Errorf("after esc from CF_DNS_API_TOKEN: cursor = %d, want %d", m2.cursor, want)
	}
	if m2.fields[m2.cursor].key != "vault_init_output_path" {
		t.Errorf("cursor %d = %q, want vault_init_output_path", m2.cursor, m2.fields[m2.cursor].key)
	}
}

func TestWizardModel_CompletionEmitsResult(t *testing.T) {
	resultCh := make(chan config.WizardResult, 1)
	m := advanceToField("BOOTSTRAP_ADMIN_PASSWORD", resultCh)

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
