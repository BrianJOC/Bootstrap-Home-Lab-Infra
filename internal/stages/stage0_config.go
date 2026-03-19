package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// ConfigStage is Stage 0: config collection and SSH validation.
type ConfigStage struct{}

// Name returns the canonical stage identifier.
func (s *ConfigStage) Name() string { return "config-collection" }

// Check always returns false — config collection always runs to validate inputs.
func (s *ConfigStage) Check(_ context.Context, _ *config.RunConfig) (bool, error) {
	return false, nil
}

// Run executes Stage 0.
func (s *ConfigStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	sink.Info(s.Name(), "validating configuration")

	// Validate the config.
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// Check SSH connectivity.
	sink.Info(s.Name(), fmt.Sprintf("testing SSH connectivity to %s", cfg.TargetHost))
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return fmt.Errorf("SSH connectivity check failed: %w", err)
	}
	defer client.Close()

	// Detect target architecture.
	sink.Info(s.Name(), "detecting target architecture")
	archOut, err := client.DetectArch()
	if err != nil {
		return fmt.Errorf("arch detection failed: %w", err)
	}
	arch := config.ParseArch(strings.TrimSpace(archOut))
	if arch == config.ArchUnknown {
		ok, verifyErr := sink.Verify(s.Name(), fmt.Sprintf("unknown architecture %q detected — continue?", strings.TrimSpace(archOut)))
		if verifyErr != nil {
			return fmt.Errorf("verify failed: %w", verifyErr)
		}
		if !ok {
			return fmt.Errorf("aborted: unknown architecture")
		}
	}
	cfg.TargetArch = arch
	sink.Info(s.Name(), fmt.Sprintf("detected arch: %s", arch))

	// Detect target OS.
	sink.Info(s.Name(), "detecting target OS")
	osName, osVersion, err := client.DetectOS()
	if err != nil {
		sink.Warn(s.Name(), fmt.Sprintf("could not detect OS: %v", err))
	} else {
		osName = strings.TrimSpace(osName)
		osVersion = strings.TrimSpace(osVersion)
		sink.Info(s.Name(), fmt.Sprintf("detected OS: %s %s", osName, osVersion))

		if osName != "Ubuntu" {
			ok, verifyErr := sink.Verify(s.Name(), fmt.Sprintf("target OS is %q, not Ubuntu — continue?", osName))
			if verifyErr != nil {
				return fmt.Errorf("verify failed: %w", verifyErr)
			}
			if !ok {
				return fmt.Errorf("aborted: unsupported OS")
			}
		}
	}

	// Write config.yaml (structural fields only, no secrets).
	sink.Info(s.Name(), "writing config.yaml")
	if err := writeConfigYAML(cfg.Config); err != nil {
		return fmt.Errorf("writing config.yaml: %w", err)
	}

	// Write .env with mode 0600.
	sink.Info(s.Name(), "writing .env")
	if err := writeEnvFile(cfg.Secrets); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	// Write run manifest.
	sink.Info(s.Name(), "writing run manifest")
	if err := writeManifest(cfg, arch); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}

	sink.Info(s.Name(), "stage=config-collection status=complete")
	return nil
}

// writeConfigYAML writes the structural config to config.yaml.
func writeConfigYAML(cfg *config.Config) error {
	// Build YAML manually to avoid serializing env-only fields.
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
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile("config.yaml", []byte(content), 0600)
}

// writeEnvFile writes the secrets to .env with mode 0600.
func writeEnvFile(sec *config.Secrets) error {
	content := fmt.Sprintf("CF_DNS_API_TOKEN=%s\nBOOTSTRAP_ADMIN_PASSWORD=%s\n",
		sec.CloudflareAPIToken,
		sec.AdminPassword,
	)
	envPath := ".env"
	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}
	if err := os.Chmod(envPath, 0600); err != nil {
		return fmt.Errorf("chmod .env to 0600: %w", err)
	}
	// Verify mode.
	fi, err := os.Stat(envPath)
	if err != nil {
		return fmt.Errorf("stat .env: %w", err)
	}
	if fi.Mode().Perm() != 0600 {
		return fmt.Errorf(".env file mode is %o, expected 0600", fi.Mode().Perm())
	}
	return nil
}

// manifest is written to logs/run-<timestamp>/manifest.json.
type manifest struct {
	TargetHost  string    `json:"target_host"`
	Arch        string    `json:"arch"`
	StartTime   time.Time `json:"start_time"`
	ConfigHash  string    `json:"config_hash"`
	PID         int       `json:"pid"`
}

// writeManifest writes the run manifest to logs/run-<timestamp>/manifest.json.
func writeManifest(cfg *config.RunConfig, arch config.TargetArch) error {
	timestamp := time.Now().UTC().Format("20060102T150405")
	runDir := "run-" + timestamp
	logDir := filepath.Join("logs", runDir)
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	m := manifest{
		TargetHost: cfg.TargetHost,
		Arch:       string(arch),
		StartTime:  time.Now().UTC(),
		ConfigHash: configHash(cfg.Config),
		PID:        os.Getpid(),
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}

	manifestPath := filepath.Join(logDir, "manifest.json")
	return os.WriteFile(manifestPath, data, 0600)
}

// configHash returns a simple hash of the config field names (not values).
func configHash(cfg *config.Config) string {
	// Return a hash of field names to indicate config schema version.
	// We use a simple sorted list of field names as the "hash".
	fields := []string{
		"target_host", "ssh_user", "ssh_key_path", "internal_domain",
		"cloudflare_zone_id", "bootstrap_server_ip", "lab_vlan_subnet",
		"lab_vlan_id", "admin_email", "vault_shares", "vault_threshold",
	}
	return strings.Join(fields, ",")
}
