// internal/tui/wizard.go
package tui

import (
	"encoding/base64"
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
// It is placed after admin_email so that the first 7 user-facing fields (0-6) can be
// collected without triggering skip logic, and field 7 is auto-accepted when the IP
// was derived from a bare target_host.
const bootstrapIPFieldIdx = 7

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
		{key: "lab_vlan_subnet", label: "Lab VLAN subnet",
			description: "CIDR subnet for your lab network. Advanced options (VLAN ID, Vault shares) can be edited by re-running with --wizard after setup.",
			example:     "10.0.0.0/24", validate: validateCIDR},
		{key: "admin_email", label: "Admin email",
			description: "Used for TLS certificate notifications and admin account creation.",
			example:     "admin@example.com", validate: validateNonEmpty},
		{key: "bootstrap_server_ip", label: "Bootstrap server IP",
			description: "IP address of this machine (used in Ansible inventory).",
			example:     "192.168.1.100", validate: validateIP},
		{key: "ssh_host_fingerprint", label: "Expected SSH host fingerprint",
			description: "Optional SHA-256 fingerprint for out-of-band identity verification. Leave empty for interactive first-use confirmation.",
			example:     "SHA256:base64-fingerprint", validate: validateOptionalSSHFingerprint},
		{key: "lab_gateway", label: "Lab gateway",
			description: "Gateway IP inside the lab subnet. The first usable address is pre-filled.",
			example:     "10.0.0.1", validate: validateIP},
		{key: "vault_init_output_path", label: "Vault recovery output path",
			description: "Absolute controller path outside this repository for Vault init JSON.",
			example:     "~/.local/share/bootstrap-home-infra/recovery/vault-init.json", validate: validateAbsolutePath},
		{key: "CF_DNS_API_TOKEN", label: "Cloudflare API token",
			description: "DNS-01 challenge token (needs Zone:DNS:Edit permission).",
			isSecret:    true, validate: validateNonEmpty},
		{key: "CONTROLLER_RECOVERY_PASSWORD", label: "Controller recovery password",
			description: "Separate password-manager secret used to encrypt the portable SSH-key envelope.",
			isSecret:    true, validate: validateNonEmpty},
		{key: "BOOTSTRAP_ADMIN_PASSWORD", label: "Admin password",
			description: "Password for the bootstrapped admin account.",
			isSecret:    true, validate: validateNonEmpty},
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
	if m.values["vault_init_output_path"] == "" {
		m.values["vault_init_output_path"] = config.DefaultConfig().VaultInitOutputPath
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
	set("ssh_host_fingerprint", rc.SSHHostFingerprint)
	set("internal_domain", rc.InternalDomain)
	set("cloudflare_zone_id", rc.CloudflareZoneID)
	set("bootstrap_server_ip", rc.BootstrapServerIP)
	set("lab_vlan_subnet", rc.LabVLANSubnet)
	set("lab_gateway", rc.LabGateway)
	set("admin_email", rc.AdminEmail)
	set("vault_init_output_path", rc.VaultInitOutputPath)
	if rc.Secrets != nil {
		if rc.Secrets.CloudflareAPIToken != "" {
			m.secrets["CF_DNS_API_TOKEN"] = rc.Secrets.CloudflareAPIToken
		}
		if rc.Secrets.AdminPassword != "" {
			m.secrets["BOOTSTRAP_ADMIN_PASSWORD"] = rc.Secrets.AdminPassword
		}
		if rc.Secrets.ControllerRecoveryPassword != "" {
			m.secrets["CONTROLLER_RECOVERY_PASSWORD"] = rc.Secrets.ControllerRecoveryPassword
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

	// Auto-accept bootstrap_server_ip when it was derived from a bare IP.
	// Ignore the user's input and use the already-stored derived value.
	if m.skipBootstrapIP && m.cursor == bootstrapIPFieldIdx {
		return m.advance()
	}

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
		if f.key == "lab_vlan_subnet" && m.values["lab_gateway"] == "" {
			if gateway := firstUsableIPv4(val); gateway != "" {
				m.values["lab_gateway"] = gateway
			}
		}
	}
	return m.advance()
}

func (m *wizardModel) advance() (screen, tea.Cmd) {
	m.cursor++

	if m.cursor >= len(m.fields) {
		var baseConfig *config.Config
		var baseSecrets *config.Secrets
		if m.preload != nil {
			baseConfig = m.preload.Config
			baseSecrets = m.preload.Secrets
		}
		result := config.WizardResult{
			Values:      make(map[string]string),
			Secrets:     make(map[string]string),
			BaseConfig:  baseConfig,
			BaseSecrets: baseSecrets,
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
		return fmt.Errorf("%q is not a valid IP address", strings.TrimSpace(s))
	}
	return nil
}

func validateCIDR(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("this field is required")
	}
	if _, _, err := net.ParseCIDR(strings.TrimSpace(s)); err != nil {
		return fmt.Errorf("%q is not a valid CIDR (e.g. 10.0.0.0/24)", strings.TrimSpace(s))
	}
	return nil
}

func validateOptionalSSHFingerprint(s string) error {
	value := strings.TrimSpace(s)
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "SHA256:") {
		return fmt.Errorf("fingerprint must start with SHA256:")
	}
	encoded := strings.TrimPrefix(value, "SHA256:")
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSuffix(encoded, "="))
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("fingerprint must contain a valid SHA-256 digest")
	}
	return nil
}

func validateAbsolutePath(s string) error {
	value := strings.TrimSpace(s)
	if !filepath.IsAbs(value) {
		return fmt.Errorf("path must be absolute")
	}
	return nil
}

func firstUsableIPv4(cidr string) string {
	ip, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return ""
	}
	base := ip.Mask(network.Mask).To4()
	if base == nil {
		return ""
	}
	gateway := append(net.IP(nil), base...)
	for i := len(gateway) - 1; i >= 0; i-- {
		gateway[i]++
		if gateway[i] != 0 {
			break
		}
	}
	return gateway.String()
}
