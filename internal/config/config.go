package config

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

var (
	internalDomainRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)
	validDataDiskRE   = regexp.MustCompile(`^/dev/(sd[a-z]+|nvme[0-9]+n[0-9]+|mmcblk[0-9]+|vd[a-z]+)$`)
	sshFingerprintRE  = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}=?$`)
	simpleIntervalRE  = regexp.MustCompile(`^[1-9][0-9]*(s|m|h|d|w)$`)
	validPagingSource = map[string]struct{}{
		"discord":  {},
		"telegram": {},
		"ntfy":     {},
		"webhook":  {},
	}
)

// Config is the strict, non-sensitive structural configuration from
// config.yaml. Defaults are applied before YAML decoding so an explicitly
// configured false value overrides a default true value.
type Config struct {
	TargetHost         string `yaml:"target_host"`
	SSHUser            string `yaml:"ssh_user"`
	SSHKeyPath         string `yaml:"ssh_key_path"`
	SSHHostFingerprint string `yaml:"ssh_host_fingerprint,omitempty"`

	InternalDomain   string `yaml:"internal_domain"`
	CloudflareZoneID string `yaml:"cloudflare_zone_id"`
	AdminEmail       string `yaml:"admin_email"`

	CertificateRenewalEnabled         bool   `yaml:"certificate_renewal_enabled"`
	CertificateRenewalOnCalendar      string `yaml:"certificate_renewal_on_calendar"`
	CertificateRenewBeforeDays        int    `yaml:"certificate_renew_before_days"`
	CertificateRenewalRandomizedDelay string `yaml:"certificate_renewal_randomized_delay"`

	BootstrapServerIP string `yaml:"bootstrap_server_ip"`
	LabVLANSubnet     string `yaml:"lab_vlan_subnet"`
	LabGateway        string `yaml:"lab_gateway"`
	LabVLANID         int    `yaml:"lab_vlan_id"`
	LabVLANInterface  string `yaml:"lab_vlan_interface,omitempty"`
	DHCPRangeLow      string `yaml:"dhcp_range_low,omitempty"`
	DHCPRangeHigh     string `yaml:"dhcp_range_high,omitempty"`
	MAASDHCPEnabled   bool   `yaml:"maas_dhcp_enabled"`

	BackupEnabled               bool   `yaml:"backup_enabled"`
	BackupRepository            string `yaml:"backup_repository,omitempty"`
	BackupOnCalendar            string `yaml:"backup_on_calendar"`
	BackupKeepDaily             int    `yaml:"backup_keep_daily"`
	BackupKeepWeekly            int    `yaml:"backup_keep_weekly"`
	BackupKeepMonthly           int    `yaml:"backup_keep_monthly"`
	BackupMetadataCheckInterval string `yaml:"backup_metadata_check_interval"`
	BackupFullCheckInterval     string `yaml:"backup_full_check_interval"`
	BackupUSBEnabled            bool   `yaml:"backup_usb_enabled"`
	BackupUSBRepository         string `yaml:"backup_usb_repository,omitempty"`
	BackupUSBCopyInterval       string `yaml:"backup_usb_copy_interval"`
	BackupRestoreDrillInterval  string `yaml:"backup_restore_drill_interval"`

	GitLabFullBackupOnCalendar      string `yaml:"gitlab_full_backup_on_calendar"`
	GitLabCheckpointAfterApply      bool   `yaml:"gitlab_checkpoint_after_apply"`
	GitLabFreezeIaCWhenUnavailable  bool   `yaml:"gitlab_freeze_iac_when_unavailable"`
	ControllerRecoveryEnabled       bool   `yaml:"controller_recovery_enabled"`
	ControllerRecoveryRefresh       bool   `yaml:"controller_recovery_refresh_after_change"`
	ControllerRecoveryIncludeSSHKey bool   `yaml:"controller_recovery_include_ssh_private_key"`

	ApplicationUpgradesEnabled      bool   `yaml:"application_upgrades_enabled"`
	UpgradeMaintenanceOnCalendar    string `yaml:"upgrade_maintenance_on_calendar"`
	UpgradeMaintenanceWindow        string `yaml:"upgrade_maintenance_admission_window"`
	UpgradeCriticalSecurityDeadline string `yaml:"upgrade_critical_security_deadline"`
	UpgradeSecurityDeadline         string `yaml:"upgrade_security_deadline"`
	UpgradePatchMinorDeadline       string `yaml:"upgrade_patch_minor_deadline"`
	UpgradeMajorDeadline            string `yaml:"upgrade_major_deadline"`
	OSSecurityUpdatesEnabled        bool   `yaml:"os_security_updates_enabled"`
	OSAutomaticReboot               bool   `yaml:"os_automatic_reboot"`

	MonitoringEnabled            bool   `yaml:"monitoring_enabled"`
	UptimeCriticalInterval       string `yaml:"uptime_critical_interval"`
	UptimeStandardInterval       string `yaml:"uptime_standard_interval"`
	UptimeRetentionDays          int    `yaml:"uptime_retention_days"`
	JournalRetentionDays         int    `yaml:"journal_retention_days"`
	ObservabilityMemoryLimitMB   int    `yaml:"observability_memory_limit_mb"`
	ObservabilityCPUQuotaPercent int    `yaml:"observability_cpu_quota_percent"`
	ObservabilityStorageLimitGB  int    `yaml:"observability_storage_limit_gb"`
	PagingEnabled                bool   `yaml:"paging_enabled"`
	PagingProvider               string `yaml:"paging_provider,omitempty"`
	PagingCriticalFailures       int    `yaml:"paging_critical_failures"`
	PagingStandardFailures       int    `yaml:"paging_standard_failures"`
	PagingResourceSustain        string `yaml:"paging_resource_sustain"`
	PagingDiskWarningPercent     int    `yaml:"paging_disk_warning_percent"`
	PagingDiskCriticalPercent    int    `yaml:"paging_disk_critical_percent"`
	PagingUnresolvedReminder     string `yaml:"paging_unresolved_reminder"`
	PagingRecoveryNotifications  bool   `yaml:"paging_recovery_notifications"`

	VaultInitOutputPath string `yaml:"vault_init_output_path"`
	VaultShares         int    `yaml:"vault_shares"`
	VaultThreshold      int    `yaml:"vault_threshold"`

	K3sEnabled     bool   `yaml:"k3s_enabled"`
	K3sClusterName string `yaml:"k3s_cluster_name"`
	K3sVersion     string `yaml:"k3s_version"`

	DataDisk            string `yaml:"data_disk,omitempty"`
	DataDiskMode        string `yaml:"data_disk_mode,omitempty"`
	DataDiskForceFormat bool   `yaml:"data_disk_force_format"`

	// Runtime-only test/API overrides.
	ACMEServerURL        string `yaml:"-"`
	ACMECACertURL        string `yaml:"-"`
	ACMEIssuingCACertURL string `yaml:"-"`
	ACMEServerHostIP     string `yaml:"-"`
	CloudflareAPIBaseURL string `yaml:"-"`
	TechnitiumAPIURL     string `yaml:"-"`
	VaultAPIURL          string `yaml:"-"`
	AuthentikAPIURL      string `yaml:"-"`
	GitLabAPIURL         string `yaml:"-"`
	GitLabBaseURL        string `yaml:"-"`
	MAASAPIURL           string `yaml:"-"`
	APIInsecure          bool   `yaml:"-"`
}

// DefaultConfig returns the accepted defaults before operator-specific values
// are applied.
func DefaultConfig() *Config {
	return &Config{
		SSHUser:                           "ubuntu",
		CertificateRenewalEnabled:         true,
		CertificateRenewalOnCalendar:      "daily",
		CertificateRenewBeforeDays:        30,
		CertificateRenewalRandomizedDelay: "1h",
		BackupEnabled:                     false,
		BackupOnCalendar:                  "*-*-* 02:30:00",
		BackupKeepDaily:                   7,
		BackupKeepWeekly:                  5,
		BackupKeepMonthly:                 12,
		BackupMetadataCheckInterval:       "weekly",
		BackupFullCheckInterval:           "monthly",
		BackupUSBCopyInterval:             "monthly",
		BackupRestoreDrillInterval:        "quarterly",
		GitLabFullBackupOnCalendar:        "*-*-* 02:30:00",
		GitLabCheckpointAfterApply:        true,
		GitLabFreezeIaCWhenUnavailable:    true,
		ControllerRecoveryEnabled:         true,
		ControllerRecoveryRefresh:         true,
		ControllerRecoveryIncludeSSHKey:   true,
		ApplicationUpgradesEnabled:        true,
		UpgradeMaintenanceOnCalendar:      "Sun *-*-* 03:00:00",
		UpgradeMaintenanceWindow:          "3h",
		UpgradeCriticalSecurityDeadline:   "3d",
		UpgradeSecurityDeadline:           "7d",
		UpgradePatchMinorDeadline:         "21d",
		UpgradeMajorDeadline:              "30d",
		OSSecurityUpdatesEnabled:          true,
		OSAutomaticReboot:                 false,
		MonitoringEnabled:                 true,
		UptimeCriticalInterval:            "60s",
		UptimeStandardInterval:            "5m",
		UptimeRetentionDays:               7,
		JournalRetentionDays:              7,
		ObservabilityMemoryLimitMB:        512,
		ObservabilityCPUQuotaPercent:      50,
		ObservabilityStorageLimitGB:       5,
		PagingEnabled:                     false,
		PagingCriticalFailures:            3,
		PagingStandardFailures:            3,
		PagingResourceSustain:             "15m",
		PagingDiskWarningPercent:          85,
		PagingDiskCriticalPercent:         95,
		PagingUnresolvedReminder:          "6h",
		PagingRecoveryNotifications:       true,
		VaultInitOutputPath:               defaultVaultInitOutputPath(),
		VaultShares:                       5,
		VaultThreshold:                    3,
		K3sEnabled:                        true,
		K3sClusterName:                    "bootstrap",
		K3sVersion:                        "v1.36.3+k3s1",
	}
}

// DataPath returns /data only for a configured full-mode secondary disk.
func (c *Config) DataPath() string {
	if c.DataDisk != "" && c.DataDiskMode != "mount_only" {
		return "/data"
	}
	return ""
}

// Secrets contains values sourced from protected environment input.
type Secrets struct {
	CloudflareAPIToken         string // CF_DNS_API_TOKEN
	AdminPassword              string // BOOTSTRAP_ADMIN_PASSWORD
	ResticPrimaryPassword      string // RESTIC_PRIMARY_PASSWORD
	ResticUSBPassword          string // RESTIC_USB_PASSWORD
	ControllerRecoveryPassword string // CONTROLLER_RECOVERY_PASSWORD
	PagingDestinationURL       string // PAGING_DESTINATION_URL
}

type TargetArch string

const (
	ArchAMD64   TargetArch = "x86_64"
	ArchARM64   TargetArch = "aarch64"
	ArchUnknown TargetArch = "unknown"
)

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

type RunConfig struct {
	*Config
	*Secrets
	TargetArch TargetArch
}

// Load decodes config.yaml strictly, loads protected environment input, and
// applies runtime-only endpoint overrides.
func Load(configPath, envPath string) (*RunConfig, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config file %q: %w", configPath, err)
	}
	if err == nil {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if decodeErr := decoder.Decode(cfg); decodeErr != nil {
			return nil, fmt.Errorf("parsing config file %q: %w", configPath, decodeErr)
		}
		var extra any
		if decodeErr := decoder.Decode(&extra); decodeErr != io.EOF {
			if decodeErr == nil {
				return nil, fmt.Errorf("parsing config file %q: multiple YAML documents are not supported", configPath)
			}
			return nil, fmt.Errorf("parsing config file %q: %w", configPath, decodeErr)
		}
	}

	if envPath != "" {
		if _, statErr := os.Stat(envPath); statErr == nil {
			if loadErr := godotenv.Load(envPath); loadErr != nil {
				return nil, fmt.Errorf("loading env file %q: %w", envPath, loadErr)
			}
		}
	}

	cfg.ACMEServerURL = envWithDefault("ACME_SERVER_URL", "https://acme-v02.api.letsencrypt.org/directory")
	cfg.ACMECACertURL = os.Getenv("ACME_CA_CERT_URL")
	cfg.ACMEIssuingCACertURL = os.Getenv("ACME_ISSUING_CA_CERT_URL")
	cfg.ACMEServerHostIP = os.Getenv("ACME_SERVER_HOST_IP")
	cfg.CloudflareAPIBaseURL = envWithDefault("CLOUDFLARE_API_BASE_URL", "https://api.cloudflare.com")
	cfg.TechnitiumAPIURL = os.Getenv("BOOTSTRAP_TECHNITIUM_API_URL")
	cfg.VaultAPIURL = os.Getenv("BOOTSTRAP_VAULT_API_URL")
	cfg.AuthentikAPIURL = os.Getenv("BOOTSTRAP_AUTHENTIK_API_URL")
	cfg.GitLabAPIURL = os.Getenv("BOOTSTRAP_GITLAB_API_URL")
	cfg.GitLabBaseURL = os.Getenv("BOOTSTRAP_GITLAB_BASE_URL")
	cfg.MAASAPIURL = os.Getenv("BOOTSTRAP_MAAS_API_URL")
	cfg.APIInsecure = os.Getenv("BOOTSTRAP_API_INSECURE") == "true"

	return &RunConfig{
		Config: cfg,
		Secrets: &Secrets{
			CloudflareAPIToken:         os.Getenv("CF_DNS_API_TOKEN"),
			AdminPassword:              os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
			ResticPrimaryPassword:      os.Getenv("RESTIC_PRIMARY_PASSWORD"),
			ResticUSBPassword:          os.Getenv("RESTIC_USB_PASSWORD"),
			ControllerRecoveryPassword: os.Getenv("CONTROLLER_RECOVERY_PASSWORD"),
			PagingDestinationURL:       os.Getenv("PAGING_DESTINATION_URL"),
		},
	}, nil
}

func Validate(rc *RunConfig) error {
	if rc == nil || rc.Config == nil || rc.Secrets == nil {
		return fmt.Errorf("configuration and protected secrets must be initialized")
	}

	required := map[string]string{
		"target_host":              rc.TargetHost,
		"ssh_user":                 rc.SSHUser,
		"ssh_key_path":             rc.SSHKeyPath,
		"internal_domain":          rc.InternalDomain,
		"cloudflare_zone_id":       rc.CloudflareZoneID,
		"bootstrap_server_ip":      rc.BootstrapServerIP,
		"lab_vlan_subnet":          rc.LabVLANSubnet,
		"lab_gateway":              rc.LabGateway,
		"admin_email":              rc.AdminEmail,
		"vault_init_output_path":   rc.VaultInitOutputPath,
		"CF_DNS_API_TOKEN":         rc.Secrets.CloudflareAPIToken,
		"BOOTSTRAP_ADMIN_PASSWORD": rc.Secrets.AdminPassword,
	}
	var missing []string
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}

	if !internalDomainRE.MatchString(rc.InternalDomain) {
		return fmt.Errorf("internal_domain %q is not a valid hostname", rc.InternalDomain)
	}
	if rc.SSHHostFingerprint != "" && !sshFingerprintRE.MatchString(rc.SSHHostFingerprint) {
		return fmt.Errorf("ssh_host_fingerprint must be an OpenSSH SHA-256 fingerprint")
	}
	if !filepath.IsAbs(rc.VaultInitOutputPath) {
		return fmt.Errorf("vault_init_output_path must be absolute")
	}

	serverIP := net.ParseIP(rc.BootstrapServerIP)
	if serverIP == nil {
		return fmt.Errorf("bootstrap_server_ip %q is not a valid IP address", rc.BootstrapServerIP)
	}
	_, labNet, err := net.ParseCIDR(rc.LabVLANSubnet)
	if err != nil {
		return fmt.Errorf("lab_vlan_subnet %q is not a valid CIDR: %w", rc.LabVLANSubnet, err)
	}
	gateway := net.ParseIP(rc.LabGateway)
	if gateway == nil || !labNet.Contains(gateway) {
		return fmt.Errorf("lab_gateway %q must be an IP inside lab_vlan_subnet", rc.LabGateway)
	}
	if rc.MAASDHCPEnabled {
		if strings.TrimSpace(rc.LabVLANInterface) == "" {
			return fmt.Errorf("lab_vlan_interface is required when maas_dhcp_enabled is true")
		}
		if err := validateDHCPRange(labNet, rc.DHCPRangeLow, rc.DHCPRangeHigh); err != nil {
			return err
		}
	}

	if rc.VaultThreshold > rc.VaultShares {
		return fmt.Errorf("vault_threshold (%d) must be <= vault_shares (%d)", rc.VaultThreshold, rc.VaultShares)
	}
	if rc.VaultShares < 3 {
		return fmt.Errorf("vault_shares must be >= 3, got %d", rc.VaultShares)
	}
	if rc.K3sEnabled {
		if strings.TrimSpace(rc.K3sClusterName) == "" {
			return fmt.Errorf("k3s_cluster_name is required when k3s_enabled is true")
		}
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(rc.K3sClusterName) {
			return fmt.Errorf("k3s_cluster_name must use lowercase letters, numbers, and hyphens")
		}
		if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$`).MatchString(rc.K3sVersion) {
			return fmt.Errorf("k3s_version must be a pinned release such as v1.36.3+k3s1")
		}
	}

	if rc.DataDisk != "" && !validDataDiskRE.MatchString(rc.DataDisk) {
		return fmt.Errorf("data_disk %q must be a supported block device path", rc.DataDisk)
	}
	if rc.DataDiskMode != "" && rc.DataDiskMode != "full" && rc.DataDiskMode != "mount_only" {
		return fmt.Errorf("data_disk_mode must be \"full\" or \"mount_only\", got %q", rc.DataDiskMode)
	}
	if rc.DataDiskMode != "" && rc.DataDisk == "" {
		return fmt.Errorf("data_disk_mode requires data_disk to be set")
	}

	if rc.CertificateRenewalEnabled {
		if rc.CertificateRenewBeforeDays <= 0 || rc.CertificateRenewalOnCalendar == "" {
			return fmt.Errorf("certificate renewal requires a positive renew-before window and schedule")
		}
		if err := validateSimpleInterval("certificate_renewal_randomized_delay", rc.CertificateRenewalRandomizedDelay); err != nil {
			return err
		}
	}

	if rc.BackupEnabled {
		if strings.TrimSpace(rc.BackupRepository) == "" {
			return fmt.Errorf("backup_repository is required when backup_enabled is true")
		}
		if rc.Secrets.ResticPrimaryPassword == "" {
			return fmt.Errorf("RESTIC_PRIMARY_PASSWORD is required when backup_enabled is true")
		}
	}
	if rc.BackupUSBEnabled {
		if strings.TrimSpace(rc.BackupUSBRepository) == "" {
			return fmt.Errorf("backup_usb_repository is required when backup_usb_enabled is true")
		}
		if rc.Secrets.ResticUSBPassword == "" {
			return fmt.Errorf("RESTIC_USB_PASSWORD is required when backup_usb_enabled is true")
		}
	}
	if rc.BackupKeepDaily < 0 || rc.BackupKeepWeekly < 0 || rc.BackupKeepMonthly < 0 {
		return fmt.Errorf("backup retention values cannot be negative")
	}
	if rc.ControllerRecoveryEnabled && rc.ControllerRecoveryIncludeSSHKey &&
		rc.Secrets.ControllerRecoveryPassword == "" {
		return fmt.Errorf("CONTROLLER_RECOVERY_PASSWORD is required for encrypted SSH-key recovery")
	}

	if rc.ApplicationUpgradesEnabled {
		for name, value := range map[string]string{
			"upgrade_maintenance_admission_window": rc.UpgradeMaintenanceWindow,
			"upgrade_critical_security_deadline":   rc.UpgradeCriticalSecurityDeadline,
			"upgrade_security_deadline":            rc.UpgradeSecurityDeadline,
			"upgrade_patch_minor_deadline":         rc.UpgradePatchMinorDeadline,
			"upgrade_major_deadline":               rc.UpgradeMajorDeadline,
		} {
			if err := validateSimpleInterval(name, value); err != nil {
				return err
			}
		}
	}

	if rc.MonitoringEnabled {
		if rc.UptimeRetentionDays <= 0 || rc.JournalRetentionDays <= 0 ||
			rc.ObservabilityMemoryLimitMB <= 0 || rc.ObservabilityCPUQuotaPercent <= 0 ||
			rc.ObservabilityCPUQuotaPercent > 100 || rc.ObservabilityStorageLimitGB <= 0 {
			return fmt.Errorf("monitoring retention and resource budgets must be positive and CPU quota <= 100")
		}
		if err := validateSimpleInterval("uptime_critical_interval", rc.UptimeCriticalInterval); err != nil {
			return err
		}
		if err := validateSimpleInterval("uptime_standard_interval", rc.UptimeStandardInterval); err != nil {
			return err
		}
	}
	if rc.PagingEnabled {
		if _, ok := validPagingSource[rc.PagingProvider]; !ok {
			return fmt.Errorf("paging_provider must be discord, telegram, ntfy, or webhook")
		}
		if rc.Secrets.PagingDestinationURL == "" {
			return fmt.Errorf("PAGING_DESTINATION_URL is required when paging_enabled is true")
		}
		if rc.PagingDiskWarningPercent <= 0 || rc.PagingDiskCriticalPercent <= rc.PagingDiskWarningPercent ||
			rc.PagingDiskCriticalPercent > 100 {
			return fmt.Errorf("paging disk thresholds must satisfy 0 < warning < critical <= 100")
		}
	}

	return nil
}

func validateDHCPRange(network *net.IPNet, lowValue, highValue string) error {
	low := net.ParseIP(lowValue)
	high := net.ParseIP(highValue)
	if low == nil || high == nil || !network.Contains(low) || !network.Contains(high) {
		return fmt.Errorf("dhcp_range_low and dhcp_range_high must be IPs inside lab_vlan_subnet")
	}
	low4, high4 := low.To4(), high.To4()
	if low4 == nil || high4 == nil {
		return fmt.Errorf("v1 MAAS DHCP range must use IPv4")
	}
	for i := range low4 {
		if low4[i] < high4[i] {
			return nil
		}
		if low4[i] > high4[i] {
			return fmt.Errorf("dhcp_range_low must be less than dhcp_range_high")
		}
	}
	return fmt.Errorf("dhcp_range_low must be less than dhcp_range_high")
}

func validateSimpleInterval(name, value string) error {
	if !simpleIntervalRE.MatchString(value) {
		return fmt.Errorf("%s %q must use a positive value with s, m, h, d, or w", name, value)
	}
	return nil
}

func envWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
