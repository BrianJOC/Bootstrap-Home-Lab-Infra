package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
	terraformrunner "github.com/homelab/bootstrap-home-infra/internal/terraform"
)

// MAASStage is Stage 7: MAAS bare metal provisioning deployment.
type MAASStage struct{}

// Name returns the canonical stage identifier.
func (s *MAASStage) Name() string { return "maas" }

// Check verifies MAAS is running and the API is accessible.
func (s *MAASStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`snap list maas >/dev/null 2>&1 || { echo not-running; exit 0; }
for attempt in 1 2 3; do
  if curl --fail --silent http://127.0.0.1:5240/MAAS/api/2.0/version/ >/dev/null; then
    echo running
    exit 0
  fi
  sleep 2
done
echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	handoffComplete, err := terraformHandoffStatus("bootstrap-maas")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(stdout) != "running" {
		if handoffComplete {
			return false, fmt.Errorf("MAAS API is unavailable after its GitLab state handoff; inspect the MAAS service instead of replaying bootstrap")
		}
		return false, nil
	}
	return handoffComplete, nil
}

// Run deploys MAAS and configures the lab VLAN via Ansible.
func (s *MAASStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	if cfg.MAASDHCPEnabled && !rc.Headless {
		approved, err := sink.Confirm(fmt.Sprintf(
			"MAAS DHCP is enabled only for lab VLAN %d (%s) on %s. Continue with preflight?",
			cfg.LabVLANID,
			cfg.LabVLANSubnet,
			cfg.LabVLANInterface,
		))
		if err != nil {
			return fmt.Errorf("confirming MAAS DHCP preflight: %w", err)
		}
		if !approved {
			return fmt.Errorf("MAAS DHCP preflight was not approved")
		}
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	if err := runner.RunPlaybook(
		"ansible/playbooks/stage-7-maas.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":     cfg.InternalDomain,
			"bootstrap_server_ip": cfg.BootstrapServerIP,
			"lab_vlan_subnet":     cfg.LabVLANSubnet,
			"lab_gateway":         cfg.LabGateway,
			"lab_vlan_id":         intToStr(cfg.LabVLANID),
			"lab_vlan_interface":  cfg.LabVLANInterface,
			"dhcp_range_low":      cfg.DHCPRangeLow,
			"dhcp_range_high":     cfg.DHCPRangeHigh,
			"maas_dhcp_enabled":   fmt.Sprintf("%t", cfg.MAASDHCPEnabled),
			"maas_admin_password": sec.AdminPassword,
			"admin_email":         cfg.AdminEmail,
		},
	); err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	maasKey, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "maas-api-key"))
	if err != nil {
		return fmt.Errorf("reading MAAS API key: %w", err)
	}
	terraformRunner := terraformrunner.New(sink, rc)
	if _, err = terraformRunner.ApplyModule(
		"bootstrap-maas",
		"terraform/environments/bootstrap/maas",
		map[string]string{
			"maas_api_url":      maasAPIURL(cfg),
			"internal_domain":   cfg.InternalDomain,
			"lab_subnet":        cfg.LabVLANSubnet,
			"lab_vlan_id":       intToStr(cfg.LabVLANID),
			"dns_server":        cfg.BootstrapServerIP,
			"lab_gateway":       cfg.LabGateway,
			"dhcp_range_low":    cfg.DHCPRangeLow,
			"dhcp_range_high":   cfg.DHCPRangeHigh,
			"maas_dhcp_enabled": fmt.Sprintf("%t", cfg.MAASDHCPEnabled),
		},
		map[string]string{"maas_api_key": strings.TrimSpace(string(maasKey))},
	); err != nil {
		return err
	}
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	trimmedMAASKey := strings.TrimSpace(string(maasKey))
	if err = storeMAASCredentialInVault(client, rootToken, trimmedMAASKey); err != nil {
		return err
	}
	projectID, err := terraformRunner.Output(
		"terraform/environments/bootstrap/gitlab",
		"bootstrap-gitlab",
		"iac_project_id",
	)
	if err != nil {
		return fmt.Errorf("reading GitLab IaC project for MAAS handoff: %w", err)
	}
	gitlabToken, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "gitlab-api-token"))
	if err != nil {
		return fmt.Errorf("reading GitLab state handoff credential: %w", err)
	}
	trimmedGitLabToken := strings.TrimSpace(string(gitlabToken))
	// The final GitOps handoff inventories provider credentials in Vault. Jobs
	// authenticate with project-bound ID tokens; no MAAS secret is stored in CI.
	return terraformRunner.MigrateStateToGitLab(
		"bootstrap-maas",
		"terraform/environments/bootstrap/maas",
		gitlabBaseURL(cfg),
		projectID,
		trimmedGitLabToken,
		cfg.APIInsecure,
	)
}
