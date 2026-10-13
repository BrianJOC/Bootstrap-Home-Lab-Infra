// Package gitops implements the project handed to GitLab. It never invokes the
// bootstrap stage runner: remote state and reviewed source are authoritative.
package gitops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Platform struct {
	Schema       int                `json:"schema"`
	Domain       string             `json:"domain"`
	Address      string             `json:"address"`
	VaultAddress string             `json:"vault_address"`
	ProjectPath  string             `json:"project_path"`
	Roots        []Root             `json:"roots"`
	Clusters     map[string]Cluster `json:"clusters"`
}

type Root struct {
	State       string            `json:"state"`
	Directory   string            `json:"directory"`
	Variables   string            `json:"variables"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

type Cluster struct {
	Address        string `json:"address"`
	KubeconfigPath string `json:"kubeconfig_vault_path"`
}

type Service struct {
	Name          string   `yaml:"name" json:"name"`
	Hostname      string   `yaml:"hostname" json:"hostname"`
	Target        Target   `yaml:"target" json:"target"`
	Authentik     *bool    `yaml:"authentik" json:"authentik"`
	AllowedGroups []string `yaml:"allowed_groups" json:"allowed_groups"`
	HealthCheck   string   `yaml:"health_check" json:"health_check"`
}

type Target struct {
	Cluster      string `yaml:"cluster" json:"cluster"`
	HelmChart    string `yaml:"helm_chart" json:"helm_chart"`
	ChartVersion string `yaml:"chart_version" json:"chart_version"`
	Repository   string `yaml:"repository,omitempty" json:"repository,omitempty"`
	Values       string `yaml:"values,omitempty" json:"values,omitempty"`
	Service      string `yaml:"service" json:"service"`
	Port         int    `yaml:"port" json:"port"`
	NodePort     int    `yaml:"node_port" json:"node_port"`
	Namespace    string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
var groupName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// LocalPath rejects symlinks as well as traversal, including links in parent
// directories. Generated projects must not ingest files from the controller.
func LocalPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || relative == "" || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") {
		return "", fmt.Errorf("path must be relative to the project: %q", relative)
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink is not allowed in project input: %s", relative)
		}
	}
	return current, nil
}

func Load(project string) (Platform, []Service, error) {
	var p Platform
	path, err := LocalPath(project, "platform/platform.json")
	if err != nil {
		return p, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return p, nil, fmt.Errorf("platform must contain exactly one JSON document")
	}
	if p.Schema != 1 || net.ParseIP(p.Address) == nil || p.ProjectPath != "infrastructure/iac" || !validDomain(p.Domain) {
		return p, nil, fmt.Errorf("invalid platform identity or unsupported schema")
	}
	parsedVault, err := url.Parse(p.VaultAddress)
	if err != nil || parsedVault.Scheme != "https" || parsedVault.Host != "vault."+p.Domain+":8200" || parsedVault.User != nil || parsedVault.Path != "" || parsedVault.RawQuery != "" || parsedVault.Fragment != "" {
		return p, nil, fmt.Errorf("Vault must use the platform HTTPS identity")
	}
	states := map[string]bool{}
	for _, r := range p.Roots {
		if !identifier.MatchString(r.State) || states[r.State] {
			return p, nil, fmt.Errorf("invalid/duplicate state: %s", r.State)
		}
		states[r.State] = true
		for _, input := range []string{r.Directory, r.Variables} {
			if _, err := LocalPath(project, input); err != nil {
				return p, nil, err
			}
		}
	}
	for name, c := range p.Clusters {
		if !identifier.MatchString(name) || net.ParseIP(c.Address) == nil || c.KubeconfigPath != "secret/infrastructure/clusters/"+name+"/kubeconfig" {
			return p, nil, fmt.Errorf("invalid cluster %s", name)
		}
	}
	for _, name := range []string{"dns", "vault", "authentik", "vault-oidc", "vault-gitlab", "gitlab", "maas"} {
		if !states["bootstrap-"+name] {
			return p, nil, fmt.Errorf("required existing state bootstrap-%s is missing", name)
		}
	}
	for name := range p.Clusters {
		if !states["k3s-"+name] {
			return p, nil, fmt.Errorf("cluster %s has no existing registration state", name)
		}
	}
	files, err := filepath.Glob(filepath.Join(project, "services", "*.yaml"))
	if err != nil {
		return p, nil, err
	}
	other, _ := filepath.Glob(filepath.Join(project, "services", "*.yml"))
	files = append(files, other...)
	sort.Strings(files)
	names, hosts, ports := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var services []Service
	for _, file := range files {
		relative, _ := filepath.Rel(project, file)
		path, err := LocalPath(project, relative)
		if err != nil {
			return p, nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return p, nil, err
		}
		var s Service
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&s); err != nil {
			return p, nil, fmt.Errorf("%s: %w", relative, err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return p, nil, fmt.Errorf("%s: exactly one service document is required", relative)
		}
		if err := s.Validate(project, p); err != nil {
			return p, nil, fmt.Errorf("%s: %w", relative, err)
		}
		portKey := fmt.Sprintf("%s:%d", s.Target.Cluster, s.Target.NodePort)
		if names[s.Name] || hosts[s.Hostname] || ports[portKey] {
			return p, nil, fmt.Errorf("duplicate service name, hostname, or gateway node_port in %s", relative)
		}
		names[s.Name], hosts[s.Hostname], ports[portKey] = true, true, true
		services = append(services, s)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return p, services, nil
}

func validDomain(value string) bool {
	if len(value) > 253 || !strings.Contains(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(label) {
			return false
		}
	}
	return true
}

func (s *Service) Validate(project string, p Platform) error {
	if !identifier.MatchString(s.Name) {
		return fmt.Errorf("name must be a lowercase DNS label, at most 40 characters")
	}
	reserved := map[string]bool{"vault": true, "gitlab": true, "authentik": true, "maas": true, "postgres": true, "technitium": true, "uptime": true, "beszel": true, "authorize": true, "token": true, "userinfo": true}
	if reserved[s.Name] {
		return fmt.Errorf("reserved platform service name %s", s.Name)
	}
	if !strings.HasSuffix(s.Hostname, "."+p.Domain) || strings.Count(s.Hostname, ".") != strings.Count(p.Domain, ".")+1 || !validDomain(s.Hostname) || reserved[strings.TrimSuffix(s.Hostname, "."+p.Domain)] {
		return fmt.Errorf("hostname must be an unreserved name covered by the platform wildcard")
	}
	if s.Authentik == nil {
		return fmt.Errorf("authentik must be explicitly true or false")
	}
	if !*s.Authentik {
		return fmt.Errorf("unauthenticated/native-protocol services require a reviewed application authorization adapter; no adapter is selected")
	}
	if len(s.AllowedGroups) == 0 {
		return fmt.Errorf("allowed_groups must not be empty")
	}
	seen := map[string]bool{}
	for _, group := range s.AllowedGroups {
		if !groupName.MatchString(group) || seen[group] {
			return fmt.Errorf("invalid or duplicate allowed group %q", group)
		}
		seen[group] = true
	}
	if _, ok := p.Clusters[s.Target.Cluster]; !ok {
		return fmt.Errorf("unknown cluster %q", s.Target.Cluster)
	}
	if s.Target.Namespace == "" {
		s.Target.Namespace = "service-" + s.Name
	}
	if s.Target.Namespace != "service-"+s.Name || !identifier.MatchString(s.Target.Service) {
		return fmt.Errorf("namespace must be service-<name> and target.service must be a DNS label")
	}
	if s.Target.Port < 1 || s.Target.Port > 65535 || s.Target.NodePort < 30000 || s.Target.NodePort > 32767 {
		return fmt.Errorf("port must be 1–65535 and node_port 30000–32767")
	}
	if s.Target.HelmChart == "" || s.Target.ChartVersion == "" || strings.ContainsAny(s.Target.ChartVersion, "*<>= \n") {
		return fmt.Errorf("helm_chart and an exact chart_version are required")
	}
	if strings.HasPrefix(s.Target.HelmChart, "charts/") {
		if _, err := LocalPath(project, filepath.Join(s.Target.HelmChart, "Chart.yaml")); err != nil {
			return err
		}
	} else {
		if !identifier.MatchString(s.Target.HelmChart) {
			return fmt.Errorf("remote helm_chart must be a chart name")
		}
		u, err := url.Parse(s.Target.Repository)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("remote charts require an HTTPS repository")
		}
	}
	if s.Target.Values != "" {
		if _, err := LocalPath(project, s.Target.Values); err != nil {
			return err
		}
	}
	if s.HealthCheck != "http" && !strings.HasPrefix(s.HealthCheck, "http:/") {
		return fmt.Errorf("web services require health_check: http or http:/path")
	}
	if s.HealthCheck != "http" {
		path, err := url.Parse(strings.TrimPrefix(s.HealthCheck, "http:"))
		if err != nil || path.Host != "" || path.RawQuery != "" || path.Fragment != "" || strings.Contains(path.Path, "..") || strings.ContainsAny(path.Path, "\r\n") {
			return fmt.Errorf("health_check must use an absolute application path")
		}
	}
	return nil
}
