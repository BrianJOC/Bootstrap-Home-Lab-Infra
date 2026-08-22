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

// K3sStage is Stage 8: k3s cluster installation.
type K3sStage struct{}

// Name returns the canonical stage identifier.
func (s *K3sStage) Name() string { return "k3s" }

// Check verifies k3s is running on the target.
func (s *K3sStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	if !cfg.K3sEnabled {
		return true, nil
	}
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`systemctl is-active k3s 2>/dev/null && kubectl get nodes --kubeconfig /etc/rancher/k3s/k3s.yaml 2>/dev/null | grep -q Ready && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	if strings.TrimSpace(stdout) != "running" {
		return false, nil
	}
	statePath, err := terraformrunner.StatePath("k3s-" + cfg.K3sClusterName)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(statePath)
	if err != nil {
		return false, nil
	}
	_, err = os.Stat(strings.TrimSuffix(statePath, ".tfstate") + ".handoff.json")
	return err == nil, nil
}

// Run deploys k3s clusters via Ansible.
func (s *K3sStage) Run(_ context.Context, cfg *config.Config, _ *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	if !cfg.K3sEnabled {
		sink.Log("info", s.Name(), "k3s is disabled; no cluster will be installed", nil)
		return nil
	}
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	if err := runner.RunPlaybook(
		"ansible/playbooks/stage-8-k3s.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":     cfg.InternalDomain,
			"bootstrap_server_ip": cfg.BootstrapServerIP,
			"k3s_cluster_name":    cfg.K3sClusterName,
			"k3s_version":         cfg.K3sVersion,
		},
	); err != nil {
		return err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	kubeconfigPath := filepath.Join(homeDir, ".local", "share", "bootstrap-home-infra", "kubeconfigs", cfg.K3sClusterName+".yaml")
	kubeconfig, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("reading protected k3s kubeconfig: %w", err)
	}
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	vaultPath := "secret/infrastructure/clusters/" + cfg.K3sClusterName + "/kubeconfig"
	vaultScript := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
KUBECONFIG_FILE="$(mktemp)"
trap 'rm -f "$KUBECONFIG_FILE"' EXIT
cat >"$KUBECONFIG_FILE"
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
vault kv put %q value=@"$KUBECONFIG_FILE" >/dev/null
`, vaultPath)
	input := append([]byte(rootToken+"\n"), kubeconfig...)
	_, stderr, err := client.RunWithStdin(vaultScript, input)
	if err != nil {
		return fmt.Errorf("storing k3s kubeconfig in Vault: %w: %s", err, strings.TrimSpace(stderr))
	}

	terraformRunner := terraformrunner.New(sink, rc)
	component := "k3s-" + cfg.K3sClusterName
	_, err = terraformRunner.ApplyModule(
		component,
		"terraform/modules/k8s-cluster",
		map[string]string{
			"cluster_name":          cfg.K3sClusterName,
			"endpoint":              "https://" + cfg.BootstrapServerIP + ":6443",
			"kubeconfig_vault_path": vaultPath,
			"nodes": fmt.Sprintf(
				`[{"name":%q,"address":%q,"roles":["control-plane","worker"]}]`,
				cfg.TargetHost,
				cfg.BootstrapServerIP,
			),
		},
		nil,
	)
	if err != nil {
		return err
	}
	projectID, err := terraformRunner.Output("terraform/modules/gitlab-config", "gitlab-config", "iac_project_id")
	if err != nil {
		return fmt.Errorf("reading GitLab IaC project for k3s handoff: %w", err)
	}
	gitlabToken, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "gitlab-api-token"))
	if err != nil {
		return fmt.Errorf("reading GitLab state handoff credential: %w", err)
	}
	return terraformRunner.MigrateStateToGitLab(
		component,
		"terraform/modules/k8s-cluster",
		"https://gitlab."+cfg.InternalDomain,
		projectID,
		strings.TrimSpace(string(gitlabToken)),
	)
}
