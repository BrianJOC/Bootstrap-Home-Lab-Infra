package gitops

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// HostRuntime is reached through the installed root-owned binary, never a
// sudo rule allowing arbitrary commands from a merge-request checkout.
func (r *Runner) HostRuntime(ctx context.Context, input io.Reader) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("host-runtime must use the installed privileged helper")
	}
	var trusted Platform
	data, err := os.ReadFile("/etc/bootstrap-home-infra/gitops-host.json")
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &trusted); err != nil {
		return err
	}
	project, err := filepath.Abs(r.Project)
	if err != nil {
		return err
	}
	r.Project = project
	p, services, err := Load(project)
	if err != nil {
		return err
	}
	if p.Domain != trusted.Domain || p.Address != trusted.Address || p.VaultAddress != trusted.VaultAddress || p.ProjectPath != trusted.ProjectPath {
		return fmt.Errorf("project identity differs from installed host identity")
	}
	r.platform = trusted
	scanner := bufio.NewScanner(io.LimitReader(input, 32768))
	if !scanner.Scan() {
		return fmt.Errorf("missing protected-branch ID token")
	}
	if err = r.login(ctx, "gitlab-apply", scanner.Text()); err != nil {
		return fmt.Errorf("host mutation authorization: %w", err)
	}
	defer func() {
		_, _ = r.api(context.Background(), "POST", trusted.VaultAddress+"/v1/auth/token/revoke-self", map[string]any{}, r.token)
	}()
	if r.Executor == nil {
		r.Executor = CommandExecutor{Output: r.Output}
	}
	if _, err = r.Executor.Run(ctx, project, []string{"/usr/local/sbin/bootstrap-gitlab-freeze-guard"}, nil, nil); err != nil {
		return err
	}
	runtime, err := r.secret(ctx, "secret/bootstrap/gitops/runtime")
	if err != nil {
		return err
	}
	vars := map[string]any{}
	data, err = os.ReadFile(filepath.Join(project, "platform/host.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &vars); err != nil {
		return err
	}
	for key, value := range runtime {
		vars[key] = value
	}
	vars["internal_domain"], vars["bootstrap_server_ip"] = trusted.Domain, trusted.Address
	vars["gitops_services"], vars["gitops_clusters"] = services, p.Clusters
	kubeconfigs := map[string]string{}
	for name, cluster := range p.Clusters {
		secret, err := r.secret(ctx, cluster.KubeconfigPath)
		if err != nil {
			return err
		}
		value, _ := secret["value"].(string)
		protected, err := ProtectedKubeconfig([]byte(value), cluster.Address)
		if err != nil {
			return err
		}
		kubeconfigs[name] = string(protected)
	}
	vars["gitops_kubeconfigs"] = kubeconfigs
	work, err := os.MkdirTemp("/run", "bootstrap-gitops-host-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	data, err = json.Marshal(vars)
	if err != nil {
		return err
	}
	varsPath := filepath.Join(work, "vars.json")
	if err = os.WriteFile(varsPath, data, 0600); err != nil {
		return err
	}
	env := map[string]string{"ANSIBLE_CONFIG": filepath.Join(project, "ansible/ansible.cfg"), "ANSIBLE_LOCAL_TEMP": work, "ANSIBLE_COLLECTIONS_PATH": filepath.Join(project, "ansible/collections")}
	_, err = r.Executor.Run(ctx, project, []string{filepath.Join(project, ".venv/bin/ansible-playbook"), "ansible/playbooks/day2.yml", "-i", "localhost,", "-c", "local", "--extra-vars", "@" + varsPath}, env, nil)
	return err
}
