package config

import (
	"fmt"
	"net"
	"strconv"
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
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a, false
		}
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
		id, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("lab_vlan_id %q is not a valid integer: %w", v, err)
		}
		cfg.LabVLANID = id
	}

	rc := &RunConfig{
		Config: cfg,
		Secrets: &Secrets{
			CloudflareAPIToken: cfToken,
			AdminPassword:      adminPass,
		},
	}
	if err := Validate(rc); err != nil {
		return nil, fmt.Errorf("wizard result failed validation: %w", err)
	}
	return rc, nil
}
