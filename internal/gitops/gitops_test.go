package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func seededProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.InternalDomain, cfg.BootstrapServerIP = "home.example.com", "192.0.2.10"
	cfg.TargetHost, cfg.K3sEnabled, cfg.K3sClusterName = "192.0.2.10", true, "bootstrap"
	if err := SeedProject("../..", project, cfg); err != nil {
		t.Fatal(err)
	}
	return project
}

func installExample(t *testing.T, project string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(project, "gitops/examples/whoami.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = writeFile(project, "services/whoami.yaml", data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSeedRoundTrip(t *testing.T) {
	project := seededProject(t)
	p, services, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Roots) != 8 || len(p.Clusters) != 1 || len(services) != 0 {
		t.Fatalf("unexpected platform: %+v", p)
	}
	for _, path := range []string{".gitlab-ci.yml", "bin/.go-1.26.1.pkg", "bin/go", "bin/helm", "ansible/playbooks/day2.yml", "terraform/versions.tf", "terraform/maas-machines.tf"} {
		if _, err := os.Stat(filepath.Join(project, path)); err != nil {
			t.Fatal(err)
		}
	}
	var manifest map[string]string
	data, _ := os.ReadFile(filepath.Join(project, "platform/seed-files.json"))
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for path, source := range manifest {
		if path != source {
			t.Fatalf("unexpected mapped source: %s", source)
		}
		if _, err := LocalPath(project, source); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{".env", "config.yaml", "vault-init.json", "test/keys/test_id_ed25519", "bin/bootstrap"} {
		if _, err := os.Stat(filepath.Join(project, path)); !os.IsNotExist(err) {
			t.Fatalf("controller input was seeded: %s", path)
		}
	}
	data, _ = os.ReadFile(filepath.Join(project, "platform/host.json"))
	if bytes.Contains(data, []byte("ssh_key_path")) || bytes.Contains(data, []byte("vault_init_output_path")) {
		t.Fatal("controller paths in host policy")
	}
	installExample(t, project)
	_, services, err = Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Target.Namespace != "service-whoami" {
		t.Fatalf("example did not load: %+v", services)
	}
}

func TestServiceInputFailsClosed(t *testing.T) {
	project := seededProject(t)
	installExample(t, project)
	path := filepath.Join(project, "services/whoami.yaml")
	original, _ := os.ReadFile(path)
	for name, data := range map[string][]byte{
		"second document":   append(append([]byte{}, original...), []byte("\n---\nname: hidden\n")...),
		"unknown field":     append(append([]byte{}, original...), []byte("\npublic: true\n")...),
		"no access adapter": bytes.ReplaceAll(original, []byte("authentik: true"), []byte("authentik: false")),
		"wrong host":        bytes.ReplaceAll(original, []byte("whoami.home.example.com"), []byte("vault.home.example.com")),
		"empty groups":      bytes.ReplaceAll(original, []byte("[adults]"), []byte("[]")),
		"missing cluster":   bytes.ReplaceAll(original, []byte("cluster: bootstrap"), []byte("cluster: missing")),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(project); err == nil {
				t.Fatal("unsafe definition was accepted")
			}
		})
	}
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(project, "services/duplicate.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(project); err == nil {
		t.Fatal("service symlink accepted")
	}
}

func TestHelmBoundary(t *testing.T) {
	valid := `apiVersion: apps/v1
kind: Deployment
metadata: {name: app}
spec:
  template:
    spec:
      automountServiceAccountToken: false
      containers:
        - name: app
          securityContext: {allowPrivilegeEscalation: false}
`
	if err := ValidateManifests([]byte(valid), "service-app"); err != nil {
		t.Fatal(err)
	}
	for name, manifest := range map[string]string{
		"ingress":       "kind: Ingress\nmetadata: {name: bypass}",
		"node port":     "kind: Service\nspec: {type: NodePort}",
		"policy":        "kind: NetworkPolicy\nspec: {ingress: [{}]}",
		"namespace":     "kind: Secret\nmetadata: {namespace: kube-system}",
		"host":          strings.Replace(valid, "automountServiceAccountToken: false", "hostNetwork: true\n      automountServiceAccountToken: false", 1),
		"token":         strings.Replace(valid, "automountServiceAccountToken: false", "automountServiceAccountToken: true", 1),
		"gateway label": strings.Replace(valid, "  template:", "  template:\n    metadata: {labels: {bootstrap-access: app-access}}", 1),
		"privilege":     strings.Replace(valid, "allowPrivilegeEscalation: false", "allowPrivilegeEscalation: true", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateManifests([]byte(manifest), "service-app"); err == nil {
				t.Fatal("bypass accepted")
			}
		})
	}
}

func TestDeletingLastServiceKeepsClusterState(t *testing.T) {
	project := seededProject(t)
	installExample(t, project)
	p, services, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	full, err := ServiceRoot(project, "/tmp/synthetic-kubeconfig", p.Domain, "bootstrap", services)
	if err != nil {
		t.Fatal(err)
	}
	if len(full["module"].(map[string]any)) != 1 {
		t.Fatal("service module missing")
	}
	empty, err := ServiceRoot(project, "/tmp/synthetic-kubeconfig", p.Domain, "bootstrap", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty["module"].(map[string]any)) != 0 || empty["provider"] == nil {
		t.Fatal("empty service set lost its state/provider root")
	}
}

type fixtureTransport struct {
	missing  bool
	requests []string
}

func (f *fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req.Method+" "+req.URL.Path)
	status, body := 200, `{}`
	switch {
	case strings.HasSuffix(req.URL.Path, "/login"):
		body = `{"auth":{"client_token":"synthetic-vault-token"}}`
	case strings.HasSuffix(req.URL.Path, "/providers"):
		body = `{"data":{"data":{"technitium_api_token":"synthetic-provider-secret","authentik_token":"synthetic-provider-secret","maas_api_key":"synthetic-provider-secret","oidc_vault_secret":"synthetic-provider-secret","gitlab_token":"synthetic-provider-secret"}}}`
	case strings.Contains(req.URL.Path, "/terraform/state/"):
		if user, pass, ok := req.BasicAuth(); !ok || user != "gitlab-ci-token" || pass != "synthetic-job-token" {
			return nil, fmt.Errorf("missing state credential")
		}
		if f.missing {
			status = 404
		}
		body = `{"version":4,"serial":1,"lineage":"fixture","resources":[]}`
	case strings.HasSuffix(req.URL.Path, "/revoke-self"):
		status, body = 204, ""
	default:
		return nil, fmt.Errorf("unexpected API request %s", req.URL)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

type fixtureExecutor struct {
	calls     []string
	failState string
	applied   map[string]bool
}

func (f *fixtureExecutor) Run(_ context.Context, dir string, args []string, env map[string]string, input io.Reader) ([]byte, error) {
	command := filepath.Base(args[0])
	if command != "terraform" {
		f.calls = append(f.calls, strings.Join(args, " "))
		return nil, nil
	}
	state := filepath.Base(env["TF_HTTP_ADDRESS"])
	f.calls = append(f.calls, state+":"+args[1])
	if _, err := os.Stat(filepath.Join(dir, "gitops_backend_override.tf")); err != nil {
		return nil, fmt.Errorf("backend missing during %s", args[1])
	}
	switch args[1] {
	case "plan":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-out=") {
				return nil, os.WriteFile(strings.TrimPrefix(arg, "-out="), []byte("synthetic-saved-plan"), 0600)
			}
		}
		return nil, fmt.Errorf("plan not saved")
	case "show":
		return []byte(`{"resource_changes":[{"address":"fixture.resource","change":{"actions":["update"],"after":{"password":"synthetic-provider-secret"}}}]}`), nil
	case "apply":
		if state == f.failState {
			return nil, errors.New("injected apply interruption")
		}
		data, err := os.ReadFile(args[len(args)-1])
		if err != nil || string(data) != "synthetic-saved-plan" {
			return nil, errors.New("did not apply saved plan")
		}
		f.applied[state] = true
	}
	return nil, nil
}

func fixtureRunner(t *testing.T) (*Runner, *fixtureExecutor, *fixtureTransport, *bytes.Buffer) {
	t.Helper()
	project := seededProject(t)
	p, _, _ := Load(project)
	p.Clusters = map[string]Cluster{}
	p.Roots = p.Roots[:len(p.Roots)-1]
	data, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(project, "platform/platform.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	executor, transport, output := &fixtureExecutor{applied: map[string]bool{}}, &fixtureTransport{}, &bytes.Buffer{}
	r := &Runner{Project: project, Executor: executor, HTTP: &http.Client{Transport: transport}, Output: output, Env: map[string]string{
		"CI_PROJECT_PATH": p.ProjectPath, "CI_PROJECT_ID": "1", "CI_API_V4_URL": "https://gitlab." + p.Domain + "/api/v4", "CI_JOB_TOKEN": "synthetic-job-token", "GITOPS_ID_TOKEN": "synthetic-jwt", "CI_COMMIT_REF_PROTECTED": "true", "CI_DEFAULT_BRANCH": "main", "CI_COMMIT_BRANCH": "main", "CI_COMMIT_SHA": "fixture-commit",
	}}
	return r, executor, transport, output
}

func TestReconcileFreshRerunAndInterruptedResume(t *testing.T) {
	r, executor, transport, output := fixtureRunner(t)
	for _, scenario := range []struct {
		name, failure string
		wantError     bool
	}{{"fresh", "", false}, {"rerun", "", false}, {"interrupted", "bootstrap-authentik", true}, {"resumed", "", false}} {
		t.Run(scenario.name, func(t *testing.T) {
			executor.calls, executor.failState = nil, scenario.failure
			err := r.Run(context.Background(), "apply")
			if (err != nil) != scenario.wantError {
				t.Fatalf("Run error = %v", err)
			}
			calls := strings.Join(executor.calls, "\n")
			if scenario.wantError && strings.Contains(calls, "bootstrap-gitops-host") {
				t.Fatal("host applied after failed Terraform")
			}
			if !scenario.wantError && (!strings.Contains(calls, "bootstrap-gitops-host") || !strings.Contains(calls, "bootstrap-gitlab-checkpoint")) {
				t.Fatal("host/checkpoint missing")
			}
			if strings.Contains(calls, "import") || strings.Contains(calls, "migrate") {
				t.Fatal("day-2 invoked bootstrap state handling")
			}
			if strings.Contains(output.String(), "synthetic-provider-secret") {
				t.Fatal("secret in summary")
			}
			if entries, _ := os.ReadDir(filepath.Join(r.Project, ".gitops")); len(entries) != 0 {
				t.Fatal("sensitive workspace retained")
			}
			if _, err := os.Stat(filepath.Join(r.Project, "terraform/environments/bootstrap/authentik/gitops_backend_override.tf")); !os.IsNotExist(err) {
				t.Fatal("override retained")
			}
			if transport.requests[len(transport.requests)-1] != "POST /v1/auth/token/revoke-self" {
				t.Fatal("Vault token not revoked")
			}
		})
	}
}

func TestReconcileRefusesMissingStateAndUnprotectedApply(t *testing.T) {
	r, executor, transport, _ := fixtureRunner(t)
	transport.missing = true
	if err := r.Run(context.Background(), "apply"); err == nil || !strings.Contains(err.Error(), "empty-state") {
		t.Fatalf("missing state: %v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatal("Terraform ran with missing remote state")
	}
	r.Env["CI_COMMIT_REF_PROTECTED"] = "false"
	transport.requests = nil
	if err := r.Run(context.Background(), "apply"); err == nil {
		t.Fatal("unprotected apply accepted")
	}
	if len(transport.requests) != 0 {
		t.Fatal("unprotected job attempted Vault login")
	}
}

func TestPlanDoesNotMutate(t *testing.T) {
	r, executor, _, _ := fixtureRunner(t)
	if err := r.Run(context.Background(), "plan"); err != nil {
		t.Fatal(err)
	}
	for _, call := range executor.calls {
		if strings.Contains(call, ":apply") || strings.Contains(call, "sudo") {
			t.Fatalf("plan mutated: %s", call)
		}
	}
}

func TestGeneratedProjectToolsAndCharts(t *testing.T) {
	if os.Getenv("GITOPS_TOOL_TEST") != "1" {
		t.Skip("run just test-gitops-generated for pinned-tool integration")
	}
	project := seededProject(t)
	installExample(t, project)
	entries, _ := os.ReadDir(filepath.Join(project, "bin"))
	for _, entry := range entries {
		if !entry.IsDir() {
			if err := os.Chmod(filepath.Join(project, "bin", entry.Name()), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	restore := exec.Command("bash", filepath.Join(project, "bin/restore-links"))
	if output, err := restore.CombinedOutput(); err != nil {
		t.Fatalf("restore Hermit links: %v: %s", err, output)
	}
	for _, tool := range []struct{ name, flag string }{{"go", "version"}, {"terraform", "version"}, {"helm", "version"}} {
		cmd := exec.Command(filepath.Join(project, "bin", tool.name), tool.flag)
		cmd.Dir = project
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seeded %s: %v: %s", tool.name, err, output)
		}
	}
	r := &Runner{Project: project, Output: io.Discard}
	if err := r.Run(context.Background(), "validate"); err != nil {
		t.Fatal(err)
	}
	unit := exec.Command(filepath.Join(project, "bin/go"), "test", "./cmd/...", "./internal/...")
	unit.Dir = project
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GITOPS_TOOL_TEST=") && !strings.HasPrefix(value, "GITOPS_DOCKER_TEST=") {
			unit.Env = append(unit.Env, value)
		}
	}
	if output, err := unit.CombinedOutput(); err != nil {
		t.Fatalf("seeded project unit tests: %v: %s", err, output)
	}
	_, services, _ := Load(project)
	document, err := ServiceRoot(project, "/tmp/synthetic-kubeconfig", "home.example.com", "bootstrap", services)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(document)
	directory := filepath.Join(project, ".gitops/validate-services")
	if err = writeFile(directory, "main.tf.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-backend=false", "-input=false"}, {"validate"}} {
		cmd := exec.Command(filepath.Join(project, "bin/terraform"), args...)
		cmd.Dir = directory
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated service root: %v: %s", err, output)
		}
	}
}
