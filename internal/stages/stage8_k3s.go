package stages

import (
	"context"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// K3sStage is Stage 8: k3s cluster installation.
type K3sStage struct{}

// Name returns the canonical stage identifier.
func (s *K3sStage) Name() string { return "k3s" }

// Check verifies k3s is running on the target.
func (s *K3sStage) Check(ctx context.Context, cfg *config.RunConfig) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
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
	return strings.TrimSpace(stdout) == "running", nil
}

// Run deploys k3s clusters via Ansible.
func (s *K3sStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-8-k3s.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":     cfg.InternalDomain,
			"bootstrap_server_ip": cfg.BootstrapServerIP,
		},
	)
}
