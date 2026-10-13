package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ServiceRoot renders one stable Terraform state per cluster, including empty
// service sets so deleting the last definition actually removes its resources.
func ServiceRoot(project, kubeconfig, domain, cluster string, services []Service) (map[string]any, error) {
	modules := map[string]any{}
	for _, s := range services {
		if s.Target.Cluster != cluster {
			continue
		}
		chart := s.Target.HelmChart
		if strings.HasPrefix(chart, "charts/") {
			chart = filepath.Join(project, chart)
		}
		values := s.Target.Values
		if values != "" {
			values = filepath.Join(project, values)
		}
		modules[s.Name] = map[string]any{
			"source": filepath.Join(project, "terraform/modules/web-service"), "name": s.Name, "hostname": s.Hostname, "namespace": s.Target.Namespace,
			"chart": chart, "chart_version": s.Target.ChartVersion, "repository": s.Target.Repository, "values_file": values,
			"upstream_service": s.Target.Service, "upstream_port": s.Target.Port, "node_port": s.Target.NodePort,
			"issuer": "https://authentik." + domain + "/application/o/" + s.Name + "/", "allowed_groups": s.AllowedGroups,
			"client_secret": "${var.client_secrets[\"" + s.Name + "\"]}",
		}
	}
	return map[string]any{
		"terraform": map[string]any{"required_version": ">= 1.9", "required_providers": map[string]any{"helm": map[string]string{"source": "hashicorp/helm", "version": "2.17.0"}, "random": map[string]string{"source": "hashicorp/random", "version": "3.6.3"}}},
		"provider":  map[string]any{"helm": map[string]any{"kubernetes": map[string]string{"config_path": kubeconfig}}},
		"variable":  map[string]any{"client_secrets": map[string]any{"type": "map(string)", "sensitive": true}},
		"module":    modules,
	}, nil
}

func (r *Runner) deployCluster(ctx context.Context, mode, name string, cluster Cluster, clients map[string]string) error {
	secret, err := r.secret(ctx, cluster.KubeconfigPath)
	if err != nil {
		return err
	}
	value, _ := secret["value"].(string)
	if value == "" {
		return fmt.Errorf("cluster %s has no kubeconfig", name)
	}
	protected, err := ProtectedKubeconfig([]byte(value), cluster.Address)
	if err != nil {
		return err
	}
	kubeconfig := filepath.Join(r.work, "kubeconfig-"+name)
	if err = os.WriteFile(kubeconfig, protected, 0600); err != nil {
		return err
	}
	directory := filepath.Join(r.work, "services-"+name)
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	document, err := ServiceRoot(r.Project, kubeconfig, r.platform.Domain, name, r.services)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(directory, "main.tf.json"), data, 0600); err != nil {
		return err
	}
	secrets := map[string]string{}
	for _, s := range r.services {
		if s.Target.Cluster != name {
			continue
		}
		if mode == "plan" {
			secrets[s.Name] = "plan-only-placeholder-never-applied"
		} else {
			if clients[s.Name] == "" {
				return fmt.Errorf("missing OIDC client secret for %s", s.Name)
			}
			secrets[s.Name] = clients[s.Name]
		}
	}
	relative, _ := filepath.Rel(r.Project, directory)
	if err = r.terraform(ctx, mode, Root{State: "services-" + name, Directory: relative}, map[string]any{"client_secrets": secrets}); err != nil {
		return err
	}
	if mode == "apply" && len(secrets) > 0 {
		client, server, err := ApplicationClient(protected)
		if err != nil {
			return err
		}
		defer client.CloseIdleConnections()
		for _, s := range r.services {
			if s.Target.Cluster != name {
				continue
			}
			if err = checkApplication(ctx, client, server, s); err != nil {
				return err
			}
			fmt.Fprintf(r.Output, "%s: application health verified\n", s.Name)
		}
	}
	return nil
}
