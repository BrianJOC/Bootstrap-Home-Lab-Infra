package gitops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateManifests is both a preflight and the Helm post-render gate. A chart
// cannot introduce another ingress, a host port, or additive network policies
// that bypass the platform's group-enforcing gateway. Unknown resource types
// need an explicit adapter before becoming part of the service contract.
func ValidateManifests(data []byte, namespace string) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("invalid chart manifest")
		}
		if len(object) == 0 {
			continue
		}
		metadata, _ := object["metadata"].(map[string]any)
		if ns, _ := metadata["namespace"].(string); ns != "" && ns != namespace {
			return fmt.Errorf("chart resource escapes dedicated service namespace")
		}
		kind, _ := object["kind"].(string)
		spec, _ := object["spec"].(map[string]any)
		switch kind {
		case "ConfigMap", "Secret", "PersistentVolumeClaim":
		case "Service":
			if value, _ := spec["type"].(string); value != "" && value != "ClusterIP" {
				return fmt.Errorf("workload Service must be ClusterIP")
			}
			if spec["externalIPs"] != nil || spec["externalName"] != nil {
				return fmt.Errorf("workload Service must not expose external addresses")
			}
		case "Deployment", "StatefulSet", "Job":
			template, _ := spec["template"].(map[string]any)
			if err := validatePod(template); err != nil {
				return err
			}
		default:
			return fmt.Errorf("chart resource %s requires a reviewed platform adapter", kind)
		}
	}
}

func validatePod(template map[string]any) error {
	metadata, _ := template["metadata"].(map[string]any)
	labels, _ := metadata["labels"].(map[string]any)
	if labels["bootstrap-access"] != nil {
		return fmt.Errorf("workload cannot impersonate the access gateway")
	}
	spec, _ := template["spec"].(map[string]any)
	if spec == nil {
		return fmt.Errorf("workload has no pod specification")
	}
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if value, _ := spec[key].(bool); value {
			return fmt.Errorf("workload cannot enable %s", key)
		}
	}
	if token, ok := spec["automountServiceAccountToken"].(bool); !ok || token {
		return fmt.Errorf("workload must disable automountServiceAccountToken")
	}
	if account, _ := spec["serviceAccountName"].(string); account != "" && account != "default" {
		return fmt.Errorf("custom service accounts require a reviewed adapter")
	}
	volumes, _ := spec["volumes"].([]any)
	for _, item := range volumes {
		volume, _ := item.(map[string]any)
		if volume["hostPath"] != nil || volume["projected"] != nil || volume["csi"] != nil {
			return fmt.Errorf("host and credential-bearing volumes require a reviewed adapter")
		}
	}
	for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
		containers, _ := spec[key].([]any)
		for _, item := range containers {
			container, _ := item.(map[string]any)
			security, _ := container["securityContext"].(map[string]any)
			if privileged, _ := security["privileged"].(bool); privileged {
				return fmt.Errorf("privileged workloads are forbidden")
			}
			if escalate, ok := security["allowPrivilegeEscalation"].(bool); !ok || escalate {
				return fmt.Errorf("workload must disable privilege escalation")
			}
			capabilities, _ := security["capabilities"].(map[string]any)
			if additions, _ := capabilities["add"].([]any); len(additions) != 0 {
				return fmt.Errorf("workload cannot add Linux capabilities")
			}
			ports, _ := container["ports"].([]any)
			for _, port := range ports {
				p, _ := port.(map[string]any)
				if p["hostPort"] != nil {
					return fmt.Errorf("workload cannot expose host ports")
				}
			}
		}
	}
	return nil
}

func (r *Runner) validateCharts(ctx context.Context) error {
	for _, s := range r.services {
		chart := s.Target.HelmChart
		if strings.HasPrefix(chart, "charts/") {
			chart = filepath.Join(r.Project, chart)
		}
		args := []string{filepath.Join(r.Project, "bin/helm"), "template", s.Name, chart, "--namespace", s.Target.Namespace, "--version", s.Target.ChartVersion}
		if s.Target.Repository != "" {
			args = append(args, "--repo", s.Target.Repository)
		}
		if s.Target.Values != "" {
			args = append(args, "--values", filepath.Join(r.Project, s.Target.Values))
		}
		output, err := r.Executor.Run(ctx, r.Project, args, nil, nil)
		if err != nil {
			return fmt.Errorf("render chart for %s: %w", s.Name, err)
		}
		if err = ValidateManifests(output, s.Target.Namespace); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return nil
}
