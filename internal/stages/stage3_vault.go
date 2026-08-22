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

// VaultStage is Stage 3: HashiCorp Vault installation and initialization.
type VaultStage struct{}

// Name returns the canonical stage identifier.
func (s *VaultStage) Name() string { return "vault" }

// Check verifies Vault is running and unsealed on the target.
func (s *VaultStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	// The remote command swallows vault/python errors (|| echo not-running) so that
	// "vault not installed yet" returns false cleanly. An SSH transport error is
	// surfaced as a real error — it indicates a connectivity problem, not a pending stage.
	stdout, _, err := client.Run(
		`vault status -format=json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print('unsealed' if not d.get('sealed', True) else 'sealed')" 2>/dev/null || echo "not-running"`,
	)
	if err != nil {
		return false, fmt.Errorf("checking vault status on target: %w", err)
	}
	return strings.TrimSpace(stdout) == "unsealed", nil
}

// Run deploys and configures Vault via Ansible.
func (s *VaultStage) Run(_ context.Context, cfg *config.Config, _ *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-3-vault.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":        cfg.InternalDomain,
			"bootstrap_server_ip":    cfg.BootstrapServerIP,
			"vault_shares":           intToStr(cfg.VaultShares),
			"vault_threshold":        intToStr(cfg.VaultThreshold),
			"admin_email":            cfg.AdminEmail,
			"vault_init_output_path": cfg.VaultInitOutputPath,
			"service_data_root":      cfg.DataPath(),
		},
	)
}
