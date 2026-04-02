package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// internalDomainRE is a strict allowlist for internal_domain values.
// Only lowercase labels with hyphens are allowed; no shell metacharacters.
var internalDomainRE = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*(\.[a-z0-9][a-z0-9\-]*)+$`)

// Config holds all non-sensitive structural configuration from config.yaml.
// No secrets. Safe to log field names (not values).
type Config struct {
	TargetHost        string `yaml:"target_host"`
	SSHUser           string `yaml:"ssh_user"`
	SSHKeyPath        string `yaml:"ssh_key_path"`
	InternalDomain    string `yaml:"internal_domain"`
	CloudflareZoneID  string `yaml:"cloudflare_zone_id"`
	BootstrapServerIP string `yaml:"bootstrap_server_ip"`
	LabVLANSubnet     string `yaml:"lab_vlan_subnet"`
	LabVLANID         int    `yaml:"lab_vlan_id"`
	AdminEmail        string `yaml:"admin_email"`
	VaultShares          int    `yaml:"vault_shares"`
	VaultThreshold       int    `yaml:"vault_threshold"`
	// Path on the controller where vault-init.json (Shamir shares + root token) is written.
	// Must be outside the repository tree. Validated in Validate().
	VaultInitOutputPath  string `yaml:"vault_init_output_path"`
	// Populated from ACME_SERVER_URL env var; default: https://acme-v02.api.letsencrypt.org/directory
	ACMEServerURL string `yaml:"-"`
	// Populated from ACME_CA_CERT_URL env var; URL to download a PEM CA cert to trust for
	// the ACME server (needed for test ACME servers like Pebble that use self-signed certs).
	ACMECACertURL string `yaml:"-"`
	// Populated from CLOUDFLARE_API_BASE_URL env var; default: https://api.cloudflare.com
	CloudflareAPIBaseURL string `yaml:"-"`
	// DataDisk is the device path of the secondary storage disk (e.g. "/dev/sda").
	// Empty means no secondary disk — all data remains on the OS disk.
	DataDisk string `yaml:"data_disk"`
	// DataDiskMode controls how the secondary disk is used.
	// "full" (default): redirects Docker root and all service data to /data.
	// "mount_only": formats and mounts at /data only; no service redirection.
	DataDiskMode string `yaml:"data_disk_mode"`
	// DataDiskForceFormat, when true, wipes the disk even if a filesystem is
	// detected (other than LABEL=homelab-data). Requires explicit operator intent.
	DataDiskForceFormat bool `yaml:"data_disk_force_format"`
}

// DataPath returns the root path for service data.
// Returns "/data" when a secondary disk is configured in full mode.
// Returns "" (empty) otherwise — Ansible roles interpret empty as "use native path".
//
// NOTE: This intentionally returns "" (not "/var/lib") when no disk is configured.
// Existing roles use /opt/... native paths; returning "/var/lib" would silently
// redirect data for single-disk targets. Empty = keep role-native defaults.
func (c *Config) DataPath() string {
	if c.DataDisk != "" && c.DataDiskMode != "mount_only" {
		return "/data"
	}
	return ""
}

// Secrets holds all sensitive values sourced from the process environment (loaded from .env).
// Values are never written to disk or logs.
type Secrets struct {
	CloudflareAPIToken string // CF_DNS_API_TOKEN
	AdminPassword      string // BOOTSTRAP_ADMIN_PASSWORD
}

// TargetArch represents the architecture of the target host.
type TargetArch string

const (
	ArchAMD64   TargetArch = "x86_64"
	ArchARM64   TargetArch = "aarch64"
	ArchUnknown TargetArch = "unknown"
)

// GoArch maps to Go's GOARCH values (used for download URL construction).
func (a TargetArch) GoArch() string {
	switch a {
	case ArchAMD64:
		return "amd64"
	case ArchARM64:
		return "arm64"
	default:
		return string(a)
	}
}

// AnsibleArch maps to Ansible's ansible_architecture fact value.
func (a TargetArch) AnsibleArch() string {
	switch a {
	case ArchAMD64:
		return "x86_64"
	case ArchARM64:
		return "aarch64"
	default:
		return string(a)
	}
}

// ParseArch parses a uname -m output into a TargetArch.
func ParseArch(unameM string) TargetArch {
	switch strings.TrimSpace(unameM) {
	case "x86_64":
		return ArchAMD64
	case "aarch64", "arm64":
		return ArchARM64
	default:
		return ArchUnknown
	}
}

// RunConfig embeds Config and Secrets along with run-time derived fields.
type RunConfig struct {
	*Config
	*Secrets
	TargetArch TargetArch
}

// Load loads config.yaml and .env, merges them into a RunConfig, and validates.
func Load(configPath, envPath string) (*RunConfig, error) {
	cfg := &Config{
		VaultShares:    5,
		VaultThreshold: 3,
		SSHUser:        "ubuntu",
	}

	// Load config.yaml if it exists.
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config file %q: %w", configPath, err)
	}
	if err == nil {
		if parseErr := yaml.Unmarshal(data, cfg); parseErr != nil {
			return nil, fmt.Errorf("parsing config file %q: %w", configPath, parseErr)
		}
	}

	// Load .env file if it exists (silently ignore if missing; validation catches missing secrets).
	if envPath != "" {
		if _, statErr := os.Stat(envPath); statErr == nil {
			if loadErr := godotenv.Load(envPath); loadErr != nil {
				return nil, fmt.Errorf("loading env file %q: %w", envPath, loadErr)
			}
		}
	}

	// Populate env-sourced fields with defaults.
	cfg.ACMEServerURL = envWithDefault("ACME_SERVER_URL", "https://acme-v02.api.letsencrypt.org/directory")
	cfg.ACMECACertURL = os.Getenv("ACME_CA_CERT_URL")
	cfg.CloudflareAPIBaseURL = envWithDefault("CLOUDFLARE_API_BASE_URL", "https://api.cloudflare.com")

	// Reflect secrets from environment.
	sec := &Secrets{
		CloudflareAPIToken: os.Getenv("CF_DNS_API_TOKEN"),
		AdminPassword:      os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}

	rc := &RunConfig{
		Config:  cfg,
		Secrets: sec,
	}

	return rc, nil
}

// Validate checks that all required fields are present and valid.
func Validate(rc *RunConfig) error {
	var missing []string

	required := map[string]string{
		"target_host":              rc.TargetHost,
		"ssh_user":                 rc.SSHUser,
		"ssh_key_path":             rc.SSHKeyPath,
		"internal_domain":          rc.InternalDomain,
		"cloudflare_zone_id":       rc.CloudflareZoneID,
		"bootstrap_server_ip":      rc.BootstrapServerIP,
		"lab_vlan_subnet":          rc.LabVLANSubnet,
		"admin_email":              rc.AdminEmail,
		"vault_init_output_path":   rc.VaultInitOutputPath,
		"CF_DNS_API_TOKEN":         rc.Secrets.CloudflareAPIToken,
		"BOOTSTRAP_ADMIN_PASSWORD": rc.Secrets.AdminPassword,
	}

	for field, val := range required {
		if strings.TrimSpace(val) == "" {
			missing = append(missing, field)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}

	// Validate internal_domain against a strict hostname allowlist to prevent
	// shell metacharacter injection in SSH commands and Ansible tasks.
	if !internalDomainRE.MatchString(rc.InternalDomain) {
		return fmt.Errorf("internal_domain %q is not a valid hostname (must match [a-z0-9-] labels)", rc.InternalDomain)
	}

	// Validate IP addresses.
	if ip := net.ParseIP(rc.BootstrapServerIP); ip == nil {
		return fmt.Errorf("bootstrap_server_ip %q is not a valid IP address", rc.BootstrapServerIP)
	}

	// Validate CIDR if provided.
	if rc.LabVLANSubnet != "" {
		if _, _, err := net.ParseCIDR(rc.LabVLANSubnet); err != nil {
			return fmt.Errorf("lab_vlan_subnet %q is not a valid CIDR: %w", rc.LabVLANSubnet, err)
		}
	}

	// Validate vault threshold <= shares.
	if rc.VaultThreshold > rc.VaultShares {
		return fmt.Errorf("vault_threshold (%d) must be <= vault_shares (%d)", rc.VaultThreshold, rc.VaultShares)
	}
	if rc.VaultShares < 3 {
		return fmt.Errorf("vault_shares must be >= 3, got %d", rc.VaultShares)
	}

	// Validate data_disk if set.
	if rc.DataDisk != "" {
		validDataDisk := regexp.MustCompile(`^/dev/(sd[a-z]+|nvme[0-9]+n[0-9]+|mmcblk[0-9]+|vd[a-z]+)$`)
		if !validDataDisk.MatchString(rc.DataDisk) {
			return fmt.Errorf("data_disk %q must be a block device path (/dev/sd*, /dev/nvme*, /dev/mmcblk*, /dev/vd*)", rc.DataDisk)
		}
	}
	if rc.DataDiskMode != "" && rc.DataDisk == "" {
		return fmt.Errorf("data_disk_mode requires data_disk to be set")
	}
	if rc.DataDiskMode != "" && rc.DataDiskMode != "full" && rc.DataDiskMode != "mount_only" {
		return fmt.Errorf("data_disk_mode must be \"full\" or \"mount_only\", got %q", rc.DataDiskMode)
	}

	return nil
}

func envWithDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
