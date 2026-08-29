package stages

import (
	"context"
	"encoding/json"
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
		`sudo docker ps --filter name=technitium --filter status=running --format '{{.Names}}' | grep -q technitium && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	handoffComplete, err := terraformHandoffStatus("bootstrap-dns")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(stdout) != "running" {
		if handoffComplete {
			return false, fmt.Errorf("Technitium is unavailable after its GitLab state handoff; inspect the DNS service instead of replaying bootstrap")
		}
		return false, nil
	}
	stdout, _, err = client.Run(fmt.Sprintf(
		`dig @127.0.0.1 vault.%[1]s +short | grep -qx %[2]q && dig @127.0.0.1 gitlab.%[1]s +short | grep -qx %[2]q`,
		cfg.InternalDomain,
		cfg.BootstrapServerIP,
	))
	if err != nil {
		if handoffComplete {
			return false, fmt.Errorf("managed DNS records are unavailable after the GitLab state handoff; reconcile them through the GitLab pipeline: %w", err)
		}
		return false, nil
	}
	return handoffComplete, nil
}

// Run deploys Technitium DNS via Ansible.
func (s *DNSStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	if err := runner.RunPlaybook(
		"ansible/playbooks/stage-1-dns.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":           cfg.InternalDomain,
			"bootstrap_server_ip":       cfg.BootstrapServerIP,
			"technitium_admin_password": sec.AdminPassword,
			"service_data_root":         cfg.DataPath(),
		},
	); err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	token, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "technitium-api-token"))
	if err != nil {
		return fmt.Errorf("reading Technitium automation token: %w", err)
	}
	records, err := json.Marshal([]map[string]any{
		{"name": "vault", "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300},
		{"name": "authentik", "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300},
		{"name": "gitlab", "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300},
		{"name": "maas", "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300},
		{"name": "postgres", "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300},
		{"name": "technitium", "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300},
	})
	if err != nil {
		return err
	}
	terraformRunner := terraformrunner.New(sink, rc)
	_, err = terraformRunner.ApplyModule(
		"bootstrap-dns",
		"terraform/environments/bootstrap/dns",
		map[string]string{
			"internal_domain": cfg.InternalDomain,
			"technitium_url":  technitiumAPIURL(cfg),
			"records":         string(records),
		},
		map[string]string{"technitium_api_token": strings.TrimSpace(string(token))},
	)
	return err
}
