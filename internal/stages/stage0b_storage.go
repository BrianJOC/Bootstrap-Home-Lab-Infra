package stages

import (
	"context"
	"fmt"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// StorageSetupStage is Stage 0b: optional secondary disk partitioning and mount.
type StorageSetupStage struct{}

// Name returns the canonical stage identifier.
func (s *StorageSetupStage) Name() string { return "storage-setup" }

// Check is a no-op — storage setup has no meaningful pre-check.
func (s *StorageSetupStage) Check(ctx context.Context, cfg *config.RunConfig) (bool, error) {
	return false, nil
}

// Run configures the secondary disk via Ansible.
// When no data disk is configured this is a no-op.
func (s *StorageSetupStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	if cfg.DataDisk == "" {
		return nil
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-0b-storage.yml",
		cfg.TargetHost+",",
		map[string]string{
			"data_disk":              cfg.DataDisk,
			"data_disk_mode":         cfg.DataDiskMode,
			"data_disk_force_format": fmt.Sprintf("%v", cfg.DataDiskForceFormat),
			"service_data_root":      cfg.DataPath(),
		},
	)
}
