package stages

import (
	"context"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// VaultStage is Stage 3: HashiCorp Vault installation and initialization.
type VaultStage struct{}

// Name returns the canonical stage identifier.
func (s *VaultStage) Name() string { return "vault" }

// Check verifies Vault is running and unsealed on the target.
func (s *VaultStage) Check(ctx context.Context, cfg *config.RunConfig) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	// Check if vault is running and unsealed.
	stdout, _, err := client.Run(
		`vault status -format=json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print('unsealed' if not d.get('sealed', True) else 'sealed')" 2>/dev/null || echo "not-running"`,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "unsealed", nil
}

// Run deploys and configures Vault via Ansible.
func (s *VaultStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-3-vault.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":     cfg.InternalDomain,
			"bootstrap_server_ip": cfg.BootstrapServerIP,
			"vault_shares":        intToStr(cfg.VaultShares),
			"vault_threshold":     intToStr(cfg.VaultThreshold),
			"admin_email":         cfg.AdminEmail,
		},
	)
}
