package stages

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/gitlab"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// K3sStage is Stage 8: k3s cluster installation — currently a deliberate, spec-consistent stub.
//
// docs/specs.md Stage 8 is IaC-driven: per-cluster definitions under
// infrastructure/clusters/*.yaml in the infrastructure/iac GitLab project drive the k3s
// install (roles/k3s_install), Vault kubeconfig writes and a Terraform apply through GitLab
// CI. That machinery does not exist yet, so this stage:
//   - skips when k3s is already running with a Ready node (Check),
//   - completes with a warning when NO cluster definitions exist (spec postcondition 6 /
//     acceptance criterion 2: "no failure"),
//   - fails loudly when cluster definitions DO exist, because deploying them is unimplemented.
type K3sStage struct{}

// Name returns the canonical stage identifier.
func (s *K3sStage) Name() string { return "k3s" }

// Check verifies k3s is running on the target.
func (s *K3sStage) Check(_ context.Context, cfg *config.Config, _ *runcontext.RunContext) (bool, error) {
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

// k3sClustersProject / k3sClustersPath / k3sClustersRef locate the cluster definitions a full
// Stage 8 implementation would consume.
const (
	k3sClustersProject = "infrastructure/iac"
	k3sClustersPath    = "infrastructure/clusters"
	k3sClustersRef     = "main"
)

// Run reports the Stage 8 stub status: it completes when no clusters are defined and fails
// when they are, so a partially configured lab can never look "deployed".
func (s *K3sStage) Run(ctx context.Context, cfg *config.Config, _ *config.Secrets, _ *runcontext.RunContext, sink stage.Sink) error {
	definitions, err := clusterDefinitions(ctx, "https://"+cfg.BootstrapServerIP)
	if err != nil {
		return err
	}

	if len(definitions) == 0 {
		sink.ShowWarningBox([]string{
			"Stage 8 (k3s) is not implemented yet: no cluster definitions found.",
			fmt.Sprintf("Looked for %s/*.yaml in %s (branch %s) on GitLab.", k3sClustersPath, k3sClustersProject, k3sClustersRef),
			"No clusters were deployed. Bootstrap is complete for the seven documented stages;",
			"implement roles/k3s_install + ansible/playbooks/stage-8-k3s.yml to enable Stage 8.",
		})
		return nil
	}

	return fmt.Errorf(
		"stage 8 (k3s) is not implemented: %d cluster definition(s) found in %s/%s (branch %s): %s",
		len(definitions), k3sClustersProject, k3sClustersPath, k3sClustersRef, strings.Join(definitions, ", "))
}

// clusterDefinitions returns the *.yaml files under the clusters path of the iac project.
// A missing tree path (GitLab answers 404) means "no clusters defined" -> (nil, nil).
func clusterDefinitions(ctx context.Context, baseURL string) ([]string, error) {
	token, err := gitlab.TokenFromHome()
	if err != nil {
		return nil, err
	}

	client := gitlab.NewClient(baseURL, token)
	entries, err := client.ListTree(ctx, k3sClustersProject, k3sClustersPath, k3sClustersRef)
	if errors.Is(err, gitlab.ErrPathNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var definitions []string
	for _, entry := range entries {
		if entry.Type == "blob" && strings.HasSuffix(entry.Name, ".yaml") {
			definitions = append(definitions, path.Base(entry.Path))
		}
	}
	return definitions, nil
}
