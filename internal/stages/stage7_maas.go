package stages

import (
	"context"
	"fmt"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
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
		`snap list maas 2>/dev/null | grep -q maas && maas status 2>/dev/null | grep -q "running" && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "running", nil
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
	return runner.RunPlaybook(
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
		},
	)
}
