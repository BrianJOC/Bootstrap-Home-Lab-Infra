package stages

import (
	"context"
	"fmt"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// TLSStage is Stage 2: Cloudflare DNS-01 cert issuance.
type TLSStage struct{}

// Name returns the canonical stage identifier.
func (s *TLSStage) Name() string { return "tls" }

// tlsRenewThreshold is the minimum remaining cert validity in seconds before
// re-issuance is triggered (30 days). Must match tls_renew_threshold_seconds in
// ansible/roles/tls_lego/defaults/main.yml.
const tlsRenewThreshold = 2592000

// Check verifies a valid certificate already exists on the target.
func (s *TLSStage) Check(_ context.Context, cfg *config.Config, _ *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return false, fmt.Errorf("connecting to target: %w", err)
	}
	defer client.Close()

	// Check if cert exists and has > 30 days remaining.
	// lego stores wildcard certs with '*' replaced by '_' in the filename.
	certPath := fmt.Sprintf("/opt/certs/certificates/_.%s.crt", cfg.InternalDomain)
	stdout, _, err := client.Run(fmt.Sprintf(
		`test -f %q && openssl x509 -noout -checkend %d -in %q 2>/dev/null && echo valid || echo missing`,
		certPath, tlsRenewThreshold, certPath,
	))
	if err != nil {
		return false, fmt.Errorf("checking certificate on target: %w", err)
	}
	return stdout == "valid\n", nil
}

// Run issues a wildcard TLS certificate via Cloudflare DNS-01.
func (s *TLSStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, _ *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	// When a non-production Cloudflare API base URL is set, use lego's httpreq
	// DNS provider instead of the cloudflare provider. The cloudflare provider
	// does not support a custom API base URL, so it always hits api.cloudflare.com.
	// httpreq is designed for custom mock endpoints (POST /present, POST /cleanup).
	dnsProvider := "cloudflare"
	httpreqEndpoint := ""
	if cfg.CloudflareAPIBaseURL != "https://api.cloudflare.com" && cfg.CloudflareAPIBaseURL != "" {
		dnsProvider = "httpreq"
		httpreqEndpoint = cfg.CloudflareAPIBaseURL
	}

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-2-tls.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":         cfg.InternalDomain,
			"cloudflare_zone_id":      cfg.CloudflareZoneID,
			"admin_email":             cfg.AdminEmail,
			"acme_server_url":         cfg.ACMEServerURL,
			"cf_dns_api_token":        sec.CloudflareAPIToken,
			"lego_dns_provider":       dnsProvider,
			"lego_httpreq_endpoint":   httpreqEndpoint,
		},
	)
}
