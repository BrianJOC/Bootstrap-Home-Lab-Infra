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
	terraformrunner "github.com/homelab/bootstrap-home-infra/internal/terraform"
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
		`export VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
status=not-running
for attempt in 1 2 3; do
  status="$(vault status -format=json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print('unsealed' if not d.get('sealed', True) else 'sealed')" 2>/dev/null || true)"
  [ "$status" = unsealed ] || [ "$status" = sealed ] || { sleep 2; continue; }
  break
done
printf '%s\n' "${status:-not-running}"`,
	)
	if err != nil {
		return false, fmt.Errorf("checking vault status on target: %w", err)
	}
	status := strings.TrimSpace(stdout)
	handoffComplete, err := terraformHandoffStatus("bootstrap-vault")
	if err != nil {
		return false, err
	}
	if status == "sealed" {
		if handoffComplete {
			return false, fmt.Errorf(
				"Vault is initialized but sealed; run `bootstrap vault unseal --config %s --env %s`, then rerun bootstrap",
				rc.ConfigPath,
				rc.EnvPath,
			)
		}
		return false, nil
	}
	if status != "unsealed" {
		if handoffComplete {
			return false, fmt.Errorf("Vault runtime is unavailable after its GitLab state handoff; inspect the Vault service instead of replaying bootstrap")
		}
		return false, nil
	}
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return false, nil
	}
	_, _, err = client.RunWithStdin(
		`IFS= read -r VAULT_TOKEN; export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true; vault kv get secret/bootstrap/tls/wildcard >/dev/null`,
		[]byte(rootToken+"\n"),
	)
	if err != nil && handoffComplete {
		return false, fmt.Errorf("Vault runtime reconciliation is incomplete after GitLab state handoff; run `bootstrap vault reconcile --config %s --env %s`: %w", rc.ConfigPath, rc.EnvPath, err)
	}
	return err == nil, nil
}

// Run deploys and configures Vault via Ansible.
func (s *VaultStage) Run(_ context.Context, cfg *config.Config, _ *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	if err := runner.RunPlaybook(
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
	); err != nil {
		return err
	}
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	terraformRunner := terraformrunner.New(sink, rc)
	if _, err = terraformRunner.ApplyModule(
		"bootstrap-vault",
		"terraform/environments/bootstrap/vault",
		map[string]string{
			"vault_address":   vaultAPIURL(cfg),
			"skip_tls_verify": boolString(cfg.APIInsecure || cfg.VaultAPIURL == ""),
		},
		map[string]string{"vault_token": rootToken},
	); err != nil {
		return err
	}
	return storeWildcardCertificateInVault(client, cfg, rootToken)
}
