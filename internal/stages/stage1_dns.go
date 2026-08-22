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

// DNSStage is Stage 1: Technitium DNS deployment.
type DNSStage struct{}

// Name returns the canonical stage identifier.
func (s *DNSStage) Name() string { return "dns" }

// Check verifies Technitium DNS is running on the target.
func (s *DNSStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`docker ps --filter name=technitium --filter status=running --format '{{.Names}}' | grep -q technitium && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "running", nil
}

// Run deploys Technitium DNS via Ansible.
func (s *DNSStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-1-dns.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":           cfg.InternalDomain,
			"bootstrap_server_ip":       cfg.BootstrapServerIP,
			"technitium_admin_password": sec.AdminPassword,
		},
	)
}
