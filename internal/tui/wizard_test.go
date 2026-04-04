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
