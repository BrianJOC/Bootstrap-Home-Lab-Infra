package stages

import (
	"context"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// AuthentikStage is Stage 4: Authentik SSO deployment.
type AuthentikStage struct{}

// Name returns the canonical stage identifier.
func (s *AuthentikStage) Name() string { return "authentik" }

// Check verifies Authentik is running and healthy on the target.
func (s *AuthentikStage) Check(ctx context.Context, cfg *config.RunConfig) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	// Check if authentik containers are running.
	stdout, _, err := client.Run(
		`docker compose -f /opt/authentik/docker-compose.yml ps --format json 2>/dev/null | python3 -c "import sys,json; data=json.load(sys.stdin) if isinstance(json.load(sys.stdin), list) else []; print('running')" 2>/dev/null || docker ps --filter name=authentik-server --filter status=running --format '{{.Names}}' | grep -q authentik && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "running", nil
}

// Run deploys Authentik via Ansible.
func (s *AuthentikStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-4-authentik.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":          cfg.InternalDomain,
			"bootstrap_server_ip":      cfg.BootstrapServerIP,
			"authentik_admin_password": cfg.Secrets.AdminPassword,
			"admin_email":              cfg.AdminEmail,
		},
	)
}
