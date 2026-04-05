package stages

import (
	"context"
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
func (s *MAASStage) Check(_ context.Context, cfg *config.Config, _ *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
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
func (s *MAASStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, _ *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
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
			"lab_vlan_id":         intToStr(cfg.LabVLANID),
			"maas_admin_password": sec.AdminPassword,
		},
	)
}
