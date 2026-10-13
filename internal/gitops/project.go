package gitops

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"gopkg.in/yaml.v3"
)

// SeedProject includes only declared source. Secrets, state, controller keys,
// provider caches, local config and test credentials are never candidates.
func SeedProject(source, destination string, cfg *config.Config) error {
	files := map[string]bool{}
	var restore strings.Builder
	restore.WriteString("#!/usr/bin/env bash\nset -Eeuo pipefail\ncd -- \"$(dirname -- \"$0\")\"\n")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		if relative == "." {
			return nil
		}
		parts := strings.Split(relative, string(filepath.Separator))
		top := parts[0]
		allowed := top == "cmd" || top == "internal" || top == "terraform" || top == "ansible" || top == "charts" || top == "gitops" || top == "bin" || top == "scripts" || top == "docs"
		if entry.IsDir() {
			if !allowed || entry.Name() == "collections" || entry.Name() == ".terraform" || entry.Name() == "molecule" {
				return filepath.SkipDir
			}
			return nil
		}
		if !allowed && relative != "go.mod" && relative != "go.sum" && relative != "requirements-dev.lock" && relative != "requirements-dev.in" && relative != "README.md" && relative != "Justfile" {
			return nil
		}
		if strings.Contains(relative, ".tfstate") || strings.HasSuffix(relative, ".tfplan") || strings.HasSuffix(relative, ".tfvars.example") || strings.HasSuffix(relative, ".tfvars.json") {
			return nil
		}
		if strings.HasSuffix(relative, ".tfvars") || strings.HasSuffix(relative, ".pem") || strings.HasSuffix(relative, ".key") || strings.HasSuffix(relative, ".pyc") || strings.HasPrefix(entry.Name(), ".env") {
			return nil
		}
		if top == "bin" && entry.Name() == "bootstrap" {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") && top != "bin" && entry.Name() != ".terraform.lock.hcl" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 && top != "bin" {
			return fmt.Errorf("source symlink not allowed: %s", relative)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.Base(target) != target || strings.ContainsAny(target+entry.Name(), "'\n\r") {
				return fmt.Errorf("invalid Hermit symlink: %s", relative)
			}
			fmt.Fprintf(&restore, "ln -sf -- '%s' '%s'\n", target, entry.Name())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = writeFile(destination, relative, data, 0644); err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = true
		return nil
	})
	if err != nil {
		return err
	}
	// GitLab's repository-file API supports regular files only. Rebuild the
	// declared Hermit links before invoking tools in a new runner checkout.
	if err = writeFile(destination, "bin/restore-links", []byte(restore.String()), 0755); err != nil {
		return err
	}
	files["bin/restore-links"] = true
	put := func(path string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		files[path] = true
		return writeFile(destination, path, append(data, '\n'), 0644)
	}
	p := Platform{Schema: 1, Domain: cfg.InternalDomain, Address: cfg.BootstrapServerIP, VaultAddress: "https://vault." + cfg.InternalDomain + ":8200", ProjectPath: "infrastructure/iac", Clusters: map[string]Cluster{}}
	add := func(name string, variables map[string]any, credentials map[string]string) error {
		path := "platform/bootstrap-" + name + ".tfvars.json"
		p.Roots = append(p.Roots, Root{State: "bootstrap-" + name, Directory: "terraform/environments/bootstrap/" + name, Variables: path, Credentials: credentials})
		return put(path, variables)
	}
	var records []map[string]any
	for _, name := range []string{"vault", "authentik", "gitlab", "maas", "postgres", "technitium"} {
		records = append(records, map[string]any{"name": name, "type": "A", "value": cfg.BootstrapServerIP, "ttl": 300})
	}
	if err = add("dns", map[string]any{"internal_domain": cfg.InternalDomain, "technitium_url": "http://" + cfg.BootstrapServerIP + ":5380", "records": records}, map[string]string{"technitium_api_token": "technitium_api_token"}); err != nil {
		return err
	}
	if err = add("vault", map[string]any{"vault_address": p.VaultAddress}, nil); err != nil {
		return err
	}
	if err = add("authentik", map[string]any{"internal_domain": cfg.InternalDomain, "authentik_url": "https://authentik." + cfg.InternalDomain, "services": []map[string]any{
		{"name": "vault", "redirect_uri": "https://vault." + cfg.InternalDomain + ":8200/ui/vault/auth/oidc/oidc/callback", "groups": []string{"adults"}},
		{"name": "gitlab", "redirect_uri": "https://gitlab." + cfg.InternalDomain + "/users/auth/openid_connect/callback", "groups": []string{"adults"}},
	}}, map[string]string{"authentik_token": "authentik_token"}); err != nil {
		return err
	}
	if err = add("vault-oidc", map[string]any{"internal_domain": cfg.InternalDomain, "vault_address": p.VaultAddress, "oidc_client_id": "vault"}, map[string]string{"oidc_client_secret": "oidc_vault_secret"}); err != nil {
		return err
	}
	if err = add("vault-gitlab", map[string]any{"internal_domain": cfg.InternalDomain, "vault_address": p.VaultAddress}, nil); err != nil {
		return err
	}
	if err = add("gitlab", map[string]any{"internal_domain": cfg.InternalDomain, "gitlab_url": "https://gitlab." + cfg.InternalDomain + "/api/v4/", "vault_address": p.VaultAddress, "maas_api_url": "http://" + cfg.BootstrapServerIP + ":5240/MAAS"}, map[string]string{"gitlab_token": "gitlab_token"}); err != nil {
		return err
	}
	if err = add("maas", map[string]any{"internal_domain": cfg.InternalDomain, "maas_api_url": "http://" + cfg.BootstrapServerIP + ":5240/MAAS", "lab_subnet": cfg.LabVLANSubnet, "lab_vlan_id": cfg.LabVLANID, "dns_server": cfg.BootstrapServerIP, "lab_gateway": cfg.LabGateway, "dhcp_range_low": cfg.DHCPRangeLow, "dhcp_range_high": cfg.DHCPRangeHigh, "maas_dhcp_enabled": cfg.MAASDHCPEnabled}, map[string]string{"maas_api_key": "maas_api_key"}); err != nil {
		return err
	}
	if cfg.K3sEnabled {
		name := cfg.K3sClusterName
		vaultPath := "secret/infrastructure/clusters/" + name + "/kubeconfig"
		p.Clusters[name] = Cluster{Address: cfg.BootstrapServerIP, KubeconfigPath: vaultPath}
		variables := "platform/k3s-" + name + ".tfvars.json"
		p.Roots = append(p.Roots, Root{State: "k3s-" + name, Directory: "terraform/modules/k8s-cluster", Variables: variables})
		if err = put(variables, map[string]any{"cluster_name": name, "endpoint": "https://" + cfg.BootstrapServerIP + ":6443", "kubeconfig_vault_path": vaultPath, "nodes": []map[string]any{{"name": cfg.TargetHost, "address": cfg.BootstrapServerIP, "roles": []string{"control-plane", "worker"}}}}); err != nil {
			return err
		}
	}
	if err = put("platform/platform.json", p); err != nil {
		return err
	}
	// Structural host policy stays editable. Controller paths and credentials are
	// excluded; the host executor obtains its runtime values from Vault.
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	var host map[string]any
	if err = yaml.Unmarshal(encoded, &host); err != nil {
		return err
	}
	for _, key := range []string{"target_host", "ssh_user", "ssh_key_path", "ssh_host_fingerprint", "vault_init_output_path", "controller_recovery_password"} {
		delete(host, key)
	}
	host["service_data_root"] = cfg.DataPath()
	host["controller_recovery_include_ssh_key"] = cfg.ControllerRecoveryIncludeSSHKey
	// Preserve explicitly selected disposable ACME/DNS endpoints across handoff.
	// Production defaults still use the normal provider and public ACME server.
	if cfg.ACMEServerURL != "" {
		host["acme_server_url"] = cfg.ACMEServerURL
	}
	if cfg.CloudflareAPIBaseURL != "" {
		host["cloudflare_api_base_url"] = cfg.CloudflareAPIBaseURL
	}
	if cfg.CloudflareAPIBaseURL != "" && cfg.CloudflareAPIBaseURL != "https://api.cloudflare.com" {
		host["lego_dns_provider"], host["lego_httpreq_endpoint"] = "httpreq", cfg.CloudflareAPIBaseURL
	}
	if cfg.ACMECACertURL != "" {
		host["lego_acme_ca_cert_url"] = cfg.ACMECACertURL
	}
	if cfg.ACMEIssuingCACertURL != "" {
		host["lego_acme_issuing_ca_cert_url"] = cfg.ACMEIssuingCACertURL
	}
	if cfg.ACMEServerHostIP != "" {
		endpoint, err := url.Parse(cfg.ACMEServerURL)
		if err != nil {
			return err
		}
		host["acme_server_host"], host["acme_server_host_ip"] = endpoint.Hostname(), cfg.ACMEServerHostIP
	}
	if err = put("platform/host.json", host); err != nil {
		return err
	}
	for _, directory := range []string{"services", "infrastructure/nodes", "infrastructure/clusters"} {
		if err = writeFile(destination, directory+"/.gitkeep", nil, 0644); err != nil {
			return err
		}
		files[directory+"/.gitkeep"] = true
	}
	data, err := os.ReadFile(filepath.Join(source, "gitops/gitlab-ci.yml"))
	if err != nil {
		return err
	}
	if err = writeFile(destination, ".gitlab-ci.yml", data, 0644); err != nil {
		return err
	}
	files[".gitlab-ci.yml"] = true
	if err = writeFile(destination, ".gitignore", []byte(".gitops/\n.venv/\nansible/collections/\n**/.terraform/\n*.tfplan\n*.tfstate*\n"), 0644); err != nil {
		return err
	}
	files[".gitignore"] = true
	for _, name := range []string{"versions.tf", "maas-machines.tf"} {
		data, err := os.ReadFile(filepath.Join(source, "terraform/modules/gitlab-config/templates", name))
		if err != nil {
			return err
		}
		if err = writeFile(destination, "terraform/"+name, data, 0644); err != nil {
			return err
		}
	}
	// Existing named seed resources retain their Terraform addresses.
	for _, path := range []string{".gitlab-ci.yml", "terraform/backend.tf.example", "terraform/versions.tf", "terraform/maas-machines.tf", "infrastructure/clusters/.gitkeep", "infrastructure/nodes/.gitkeep"} {
		delete(files, path)
	}
	manifest := map[string]string{}
	for path := range files {
		manifest[path] = path
	}
	// The manifest itself is seeded so day-2 plans have the same file addresses.
	manifest["platform/seed-files.json"] = "platform/seed-files.json"
	return put("platform/seed-files.json", manifest)
}

func writeFile(root, path string, data []byte, mode fs.FileMode) error {
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	return os.WriteFile(target, data, mode)
}
