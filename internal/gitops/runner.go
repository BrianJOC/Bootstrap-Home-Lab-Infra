package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Executor is injectable so integration fixtures can exercise the real
// orchestration without granting a mock access to a homelab.
type Executor interface {
	Run(context.Context, string, []string, map[string]string, io.Reader) ([]byte, error)
}

type CommandExecutor struct{ Output io.Writer }

func (e CommandExecutor) Run(ctx context.Context, dir string, args []string, env map[string]string, input io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir, cmd.Stdin = dir, input
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "TF_") || strings.HasPrefix(key, "VAULT_") || key == "GITOPS_ID_TOKEN" {
			continue
		}
		if _, ok := env[key]; !ok {
			cmd.Env = append(cmd.Env, item)
		}
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	// Never stream raw provider output: error responses and plan JSON may carry
	// credentials despite Terraform's sensitive annotation. Publish only the
	// resource-address/action summary produced below.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed (%s); sensitive command output withheld", filepath.Base(args[0]), err)
	}
	return output, nil
}

type Runner struct {
	Project     string
	Executor    Executor
	HTTP        *http.Client
	Output      io.Writer
	Env         map[string]string
	platform    Platform
	services    []Service
	token       string
	credentials map[string]string
	clients     map[string]string
	work        string
}

func (r *Runner) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 45 * time.Second}
}
func (r *Runner) api(ctx context.Context, method, address string, body any, token string) (map[string]any, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed for %s", req.URL.Host)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API %s returned HTTP %d", req.URL.Path, resp.StatusCode)
	}
	var result map[string]any
	if resp.StatusCode == 204 {
		return result, nil
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("API returned an invalid JSON response")
	}
	return result, nil
}

func (r *Runner) login(ctx context.Context, role, jwt string) error {
	if jwt == "" {
		return fmt.Errorf("GitLab OIDC ID token is required")
	}
	result, err := r.api(ctx, "POST", r.platform.VaultAddress+"/v1/auth/jwt/login", map[string]string{"role": role, "jwt": jwt}, "")
	if err != nil {
		return err
	}
	auth, _ := result["auth"].(map[string]any)
	r.token, _ = auth["client_token"].(string)
	if r.token == "" {
		return fmt.Errorf("Vault login returned no token")
	}
	return nil
}

func (r *Runner) secret(ctx context.Context, path string) (map[string]any, error) {
	if !strings.HasPrefix(path, "secret/") {
		return nil, fmt.Errorf("invalid KV secret path")
	}
	response, err := r.api(ctx, "GET", r.platform.VaultAddress+"/v1/secret/data/"+strings.TrimPrefix(path, "secret/"), nil, r.token)
	if err != nil {
		return nil, err
	}
	outer, _ := response["data"].(map[string]any)
	data, _ := outer["data"].(map[string]any)
	if data == nil {
		return nil, fmt.Errorf("Vault secret has no data: %s", path)
	}
	return data, nil
}

func (r *Runner) Run(ctx context.Context, mode string) error {
	if mode != "validate" && mode != "plan" && mode != "apply" {
		return fmt.Errorf("mode must be validate, plan or apply")
	}
	project, err := filepath.Abs(r.Project)
	if err != nil {
		return err
	}
	r.Project = project
	r.platform, r.services, err = Load(project)
	if err != nil {
		return err
	}
	if r.Output == nil {
		r.Output = io.Discard
	}
	if r.Executor == nil {
		r.Executor = CommandExecutor{Output: r.Output}
	}
	if err = r.validateCharts(ctx); err != nil {
		return err
	}
	if mode == "validate" {
		fmt.Fprintf(r.Output, "Validated %d services and %d existing state roots.\n", len(r.services), len(r.platform.Roots))
		return nil
	}
	if r.Env["CI_PROJECT_PATH"] != r.platform.ProjectPath {
		return fmt.Errorf("job project does not match platform ownership")
	}
	apiURL, err := url.Parse(r.Env["CI_API_V4_URL"])
	if err != nil || apiURL.Scheme != "https" || apiURL.Host != "gitlab."+r.platform.Domain || apiURL.Path != "/api/v4" || apiURL.User != nil || apiURL.RawQuery != "" || apiURL.Fragment != "" || r.Env["CI_PROJECT_ID"] == "" || strings.Trim(r.Env["CI_PROJECT_ID"], "0123456789") != "" || r.Env["CI_JOB_TOKEN"] == "" {
		return fmt.Errorf("state backend must use the platform GitLab identity and CI job credentials")
	}
	if mode == "apply" && (r.Env["CI_COMMIT_REF_PROTECTED"] != "true" || r.Env["CI_COMMIT_BRANCH"] != r.Env["CI_DEFAULT_BRANCH"] || r.Env["CI_DEFAULT_BRANCH"] == "") {
		return fmt.Errorf("mutation requires the protected default branch")
	}
	if err = r.login(ctx, "gitlab-"+mode, r.Env["GITOPS_ID_TOKEN"]); err != nil {
		return err
	}
	defer func() {
		_, _ = r.api(context.Background(), "POST", r.platform.VaultAddress+"/v1/auth/token/revoke-self", map[string]any{}, r.token)
	}()
	providers, err := r.secret(ctx, "secret/bootstrap/gitops/providers")
	if err != nil {
		return err
	}
	r.credentials = map[string]string{}
	for key, value := range providers {
		if s, ok := value.(string); ok {
			r.credentials[key] = s
		}
	}
	if err = os.MkdirAll(filepath.Join(project, ".gitops"), 0700); err != nil {
		return err
	}
	r.work, err = os.MkdirTemp(filepath.Join(project, ".gitops"), "run-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(r.work)
	for _, root := range r.platform.Roots {
		variables, err := r.variables(root)
		if err != nil {
			return err
		}
		if err = r.terraform(ctx, mode, root, variables); err != nil {
			return fmt.Errorf("%s: %w", root.State, err)
		}
	}
	// Registration/deployment remains independently owned in the existing MAAS
	// state. Cluster join is deliberately not part of this command.
	if _, err = os.Stat(filepath.Join(project, "terraform/maas-machines.tf")); err == nil {
		root := Root{State: "maas-machines", Directory: "terraform", Credentials: map[string]string{"MAAS_API_KEY": "maas_api_key"}}
		if err = r.terraform(ctx, mode, root, map[string]any{}); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(r.platform.Clusters))
	for name := range r.platform.Clusters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cluster := r.platform.Clusters[name]
		if err = r.deployCluster(ctx, mode, name, cluster, r.clients); err != nil {
			return err
		}
	}
	if mode == "apply" {
		// Only the installed, root-owned helper may cross into host privilege.
		// It independently authenticates the signed ID token with gitlab-apply.
		if _, err = r.Executor.Run(ctx, project, []string{"sudo", "-n", "/usr/local/sbin/bootstrap-gitops-host", project}, map[string]string{}, strings.NewReader(r.Env["GITOPS_ID_TOKEN"]+"\n")); err != nil {
			return err
		}
		if err = r.checkServices(ctx); err != nil {
			return err
		}
		if _, err = r.Executor.Run(ctx, project, []string{"sudo", "-n", "/usr/local/sbin/bootstrap-gitlab-checkpoint"}, nil, nil); err != nil {
			return err
		}
	}
	fmt.Fprintf(r.Output, "GitOps %s completed for %s.\n", mode, r.Env["CI_COMMIT_SHA"])
	return nil
}

func (r *Runner) variables(root Root) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(r.Project, root.Variables))
	if err != nil {
		return nil, err
	}
	v := map[string]any{}
	if err = json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	switch root.State {
	case "bootstrap-gitlab":
		v["iac_source_dir"] = r.Project
	case "bootstrap-dns":
		records, _ := v["records"].([]any)
		for _, s := range r.services {
			records = append(records, map[string]any{"name": strings.TrimSuffix(s.Hostname, "."+r.platform.Domain), "type": "A", "value": r.platform.Address, "ttl": 300})
		}
		v["records"] = records
	case "bootstrap-authentik":
		services, _ := v["services"].([]any)
		for _, s := range r.services {
			services = append(services, map[string]any{"name": s.Name, "redirect_uri": "https://" + s.Hostname + "/oauth2/callback", "groups": s.AllowedGroups})
		}
		v["services"] = services
	}
	return v, nil
}

func (r *Runner) command(ctx context.Context, root Root, args []string, extra map[string]string) ([]byte, error) {
	env := map[string]string{"TF_IN_AUTOMATION": "true", "TF_INPUT": "false", "TF_DATA_DIR": filepath.Join(r.work, "data", root.State), "TF_VAR_vault_token": r.token, "VAULT_TOKEN": r.token, "TF_HTTP_USERNAME": "gitlab-ci-token", "TF_HTTP_PASSWORD": r.Env["CI_JOB_TOKEN"]}
	base := strings.TrimSuffix(r.Env["CI_API_V4_URL"], "/") + "/projects/" + r.Env["CI_PROJECT_ID"] + "/terraform/state/" + root.State
	env["TF_HTTP_ADDRESS"], env["TF_HTTP_LOCK_ADDRESS"], env["TF_HTTP_UNLOCK_ADDRESS"] = base, base+"/lock", base+"/lock"
	env["TF_HTTP_LOCK_METHOD"], env["TF_HTTP_UNLOCK_METHOD"] = "POST", "DELETE"
	for variable, field := range root.Credentials {
		value, ok := r.credentials[field]
		if !ok || value == "" {
			return nil, fmt.Errorf("missing Vault credential field %s", field)
		}
		if strings.HasPrefix(variable, "MAAS_") {
			env[variable] = value
		} else {
			env["TF_VAR_"+variable] = value
		}
	}
	env["MAAS_API_URL"] = "http://" + r.platform.Address + ":5240/MAAS"
	for key, value := range extra {
		env[key] = value
	}
	return r.Executor.Run(ctx, filepath.Join(r.Project, root.Directory), append([]string{filepath.Join(r.Project, "bin/terraform")}, args...), env, nil)
}

func (r *Runner) terraform(ctx context.Context, mode string, root Root, variables map[string]any) error {
	// A job-local override selects the existing HTTP backend and is removed on
	// every return. Day-2 never migrates controller state or imports resources.
	directory := filepath.Join(r.Project, root.Directory)
	if err := osMkdir(directory); err != nil {
		return err
	}
	backend := filepath.Join(directory, "gitops_backend_override.tf")
	if _, err := os.Lstat(backend); err == nil {
		return fmt.Errorf("unexpected backend override already exists: %s", root.Directory)
	}
	if err := os.WriteFile(backend, []byte("terraform {\n  backend \"http\" {}\n}\n"), 0600); err != nil {
		return err
	}
	defer os.Remove(backend)
	if strings.HasPrefix(root.State, "bootstrap-") || strings.HasPrefix(root.State, "k3s-") {
		request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(r.Env["CI_API_V4_URL"], "/")+"/projects/"+r.Env["CI_PROJECT_ID"]+"/terraform/state/"+root.State, nil)
		if err != nil {
			return err
		}
		request.SetBasicAuth("gitlab-ci-token", r.Env["CI_JOB_TOKEN"])
		response, err := r.client().Do(request)
		if err != nil {
			return fmt.Errorf("cannot verify existing remote state %s", root.State)
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("existing remote state %s is unavailable (HTTP %d); refusing empty-state recreation", root.State, response.StatusCode)
		}
	}
	if _, err := r.command(ctx, root, []string{"init", "-reconfigure", "-input=false", "-no-color"}, nil); err != nil {
		return err
	}
	varsPath := filepath.Join(r.work, root.State+".tfvars.json")
	data, err := json.Marshal(variables)
	if err != nil {
		return err
	}
	if err = os.WriteFile(varsPath, data, 0600); err != nil {
		return err
	}
	plan := filepath.Join(r.work, root.State+".tfplan")
	if _, err = r.command(ctx, root, []string{"plan", "-input=false", "-no-color", "-out=" + plan, "-var-file=" + varsPath}, nil); err != nil {
		return err
	}
	output, err := r.command(ctx, root, []string{"show", "-json", plan}, nil)
	if err != nil {
		return err
	}
	var summary struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Change  struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err = json.Unmarshal(output, &summary); err != nil {
		return fmt.Errorf("invalid saved-plan JSON")
	}
	for _, change := range summary.ResourceChanges {
		if len(change.Change.Actions) == 1 && change.Change.Actions[0] == "no-op" {
			continue
		}
		fmt.Fprintf(r.Output, "%s: %s %s\n", root.State, strings.Join(change.Change.Actions, "/"), change.Address)
	}
	if mode == "apply" {
		if _, err = r.Executor.Run(ctx, r.Project, []string{"sudo", "-n", "/usr/local/sbin/bootstrap-gitlab-freeze-guard"}, nil, nil); err != nil {
			return err
		}
		_, err = r.command(ctx, root, []string{"apply", "-input=false", "-no-color", plan}, nil)
		if err == nil && root.State == "bootstrap-authentik" && len(r.services) > 0 {
			var output []byte
			output, err = r.command(ctx, root, []string{"output", "-json", "client_secrets"}, nil)
			if err == nil {
				err = json.Unmarshal(output, &r.clients)
			}
		}
	}
	return err
}

func osMkdir(path string) error { return os.MkdirAll(path, 0700) }

func (r *Runner) checkServices(ctx context.Context) error {
	client := *r.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, s := range r.services {
		request, _ := http.NewRequestWithContext(ctx, "GET", "https://"+s.Hostname+"/", nil)
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("service %s failed trusted HTTPS check", s.Name)
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		response.Body.Close()
		location, locationErr := response.Location()
		if response.StatusCode != 302 || locationErr != nil || location.Scheme != "https" || location.Host != "authentik."+r.platform.Domain || !strings.HasPrefix(location.Path, "/application/o/") {
			return fmt.Errorf("service %s did not redirect to the platform login (HTTP %d)", s.Name, response.StatusCode)
		}
		fmt.Fprintf(r.Output, "%s: trusted HTTPS and authentication boundary verified\n", s.Name)
	}
	return nil
}
