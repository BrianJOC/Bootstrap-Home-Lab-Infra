package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/recovery"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

type ControllerRecoveryStage struct{}

func (s *ControllerRecoveryStage) Name() string { return "controller-recovery" }

func (s *ControllerRecoveryStage) Check(_ context.Context, cfg *config.Config, _ *runcontext.RunContext) (bool, error) {
	return !cfg.ControllerRecoveryEnabled || !cfg.ControllerRecoveryRefresh, nil
}

func (s *ControllerRecoveryStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	if !cfg.ControllerRecoveryEnabled {
		sink.Log("info", s.Name(), "portable controller recovery is disabled", nil)
		return nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving recovery directory: %w", err)
	}
	bundlePath := filepath.Join(
		homeDir,
		".local",
		"share",
		"bootstrap-home-infra",
		"recovery",
		"controller-bundle.tar.gz",
	)
	if err := recovery.Create(bundlePath, recovery.BundleOptions{
		Config:         cfg,
		KnownHostsPath: rc.KnownHostsPath,
		SSHKeyPath:     cfg.SSHKeyPath,
		Password:       sec.ControllerRecoveryPassword,
		IncludeSSHKey:  cfg.ControllerRecoveryIncludeSSHKey,
	}); err != nil {
		return err
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()
	if _, _, err := client.Run("sudo install -d -m 0700 /var/lib/bootstrap-home-infra/controller-recovery && sudo chown \"$(id -u):$(id -g)\" /var/lib/bootstrap-home-infra/controller-recovery"); err != nil {
		return fmt.Errorf("preparing remote controller recovery directory: %w", err)
	}
	if err := client.WriteFile(
		"/var/lib/bootstrap-home-infra/controller-recovery/controller-bundle.tar.gz",
		bundle,
		0600,
	); err != nil {
		return err
	}
	sink.Log("info", s.Name(), "refreshed encrypted portable controller recovery bundle", map[string]string{"path": bundlePath})
	return nil
}
