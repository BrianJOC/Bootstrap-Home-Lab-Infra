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

// StorageSetupStage is Stage 0b: optional secondary disk partitioning and mount.
type StorageSetupStage struct{}

// Name returns the canonical stage identifier.
func (s *StorageSetupStage) Name() string { return "storage-setup" }

// Check is a no-op — storage setup has no meaningful pre-check.
func (s *StorageSetupStage) Check(_ context.Context, _ *config.Config, _ *runcontext.RunContext) (bool, error) {
	return false, nil
}

// Run configures the secondary disk via Ansible.
// When no data disk is configured this is a no-op.
func (s *StorageSetupStage) Run(_ context.Context, cfg *config.Config, _ *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	if cfg.DataDisk == "" {
		return nil
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	// Normalize empty mode to "full" — Ansible defaults/main.yml also
	// defaults to "full", but extra-vars override defaults; an empty string
	// would reach the role and produce ambiguous behaviour.
	mode := cfg.DataDiskMode
	if mode == "" {
		mode = "full"
	}

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-0b-storage.yml",
		cfg.TargetHost+",",
		map[string]string{
			"data_disk":              cfg.DataDisk,
			"data_disk_mode":         mode,
			"data_disk_force_format": fmt.Sprintf("%v", cfg.DataDiskForceFormat),
			"service_data_root":      cfg.DataPath(),
		},
	)
}
