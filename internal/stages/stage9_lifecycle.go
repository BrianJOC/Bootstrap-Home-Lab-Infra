package stages

import (
	"context"
	"fmt"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// LifecycleStage installs target-host backup, maintenance, observability, and
// paging controls after the platform services they inspect are present.
type LifecycleStage struct{}

func (s *LifecycleStage) Name() string { return "lifecycle" }

func (s *LifecycleStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	checks := []string{"test -f /var/lib/bootstrap-home-infra/lifecycle-configured"}
	if cfg.BackupEnabled {
		checks = append(checks, "systemctl is-enabled --quiet bootstrap-backup.timer")
	} else {
		checks = append(checks, "test -f /var/lib/bootstrap-home-infra/backup-disabled")
	}
	if cfg.ApplicationUpgradesEnabled {
		checks = append(checks, "systemctl is-enabled --quiet bootstrap-maintenance.timer")
	}
	if cfg.MonitoringEnabled {
		checks = append(checks, "systemctl is-active --quiet bootstrap-observability.service")
	}

	_, _, err = client.Run(strings.Join(checks, " && "))
	return err == nil, nil
}

func (s *LifecycleStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	retentionApproved := false
	if cfg.BackupEnabled && !rc.Headless {
		approved, err := sink.Confirm(fmt.Sprintf(
			"Approve scheduled Restic retention at %d daily / %d weekly / %d monthly after repository identity validation?",
			cfg.BackupKeepDaily,
			cfg.BackupKeepWeekly,
			cfg.BackupKeepMonthly,
		))
		if err != nil {
			return fmt.Errorf("confirming backup retention: %w", err)
		}
		retentionApproved = approved
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-9-lifecycle.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":                         cfg.InternalDomain,
			"bootstrap_server_ip":                     cfg.BootstrapServerIP,
			"backup_enabled":                          fmt.Sprintf("%t", cfg.BackupEnabled),
			"backup_repository":                       cfg.BackupRepository,
			"backup_on_calendar":                      cfg.BackupOnCalendar,
			"backup_keep_daily":                       intToStr(cfg.BackupKeepDaily),
			"backup_keep_weekly":                      intToStr(cfg.BackupKeepWeekly),
			"backup_keep_monthly":                     intToStr(cfg.BackupKeepMonthly),
			"backup_metadata_check_interval":          cfg.BackupMetadataCheckInterval,
			"backup_full_check_interval":              cfg.BackupFullCheckInterval,
			"backup_retention_approved":               fmt.Sprintf("%t", retentionApproved),
			"backup_usb_enabled":                      fmt.Sprintf("%t", cfg.BackupUSBEnabled),
			"backup_usb_repository":                   cfg.BackupUSBRepository,
			"backup_usb_copy_interval":                cfg.BackupUSBCopyInterval,
			"restic_primary_password":                 sec.ResticPrimaryPassword,
			"restic_usb_password":                     sec.ResticUSBPassword,
			"application_upgrades_enabled":            fmt.Sprintf("%t", cfg.ApplicationUpgradesEnabled),
			"upgrade_maintenance_on_calendar":         cfg.UpgradeMaintenanceOnCalendar,
			"upgrade_maintenance_admission_window":    cfg.UpgradeMaintenanceWindow,
			"upgrade_critical_security_deadline":      cfg.UpgradeCriticalSecurityDeadline,
			"upgrade_security_deadline":               cfg.UpgradeSecurityDeadline,
			"upgrade_patch_minor_deadline":            cfg.UpgradePatchMinorDeadline,
			"upgrade_major_deadline":                  cfg.UpgradeMajorDeadline,
			"os_security_updates_enabled":             fmt.Sprintf("%t", cfg.OSSecurityUpdatesEnabled),
			"os_automatic_reboot":                     fmt.Sprintf("%t", cfg.OSAutomaticReboot),
			"monitoring_enabled":                      fmt.Sprintf("%t", cfg.MonitoringEnabled),
			"uptime_retention_days":                   intToStr(cfg.UptimeRetentionDays),
			"journal_retention_days":                  intToStr(cfg.JournalRetentionDays),
			"observability_memory_limit_mb":           intToStr(cfg.ObservabilityMemoryLimitMB),
			"observability_cpu_quota_percent":         intToStr(cfg.ObservabilityCPUQuotaPercent),
			"observability_storage_limit_gb":          intToStr(cfg.ObservabilityStorageLimitGB),
			"paging_enabled":                          fmt.Sprintf("%t", cfg.PagingEnabled),
			"paging_provider":                         cfg.PagingProvider,
			"paging_destination_url":                  sec.PagingDestinationURL,
			"paging_critical_failures":                intToStr(cfg.PagingCriticalFailures),
			"paging_disk_warning_percent":             intToStr(cfg.PagingDiskWarningPercent),
			"paging_disk_critical_percent":            intToStr(cfg.PagingDiskCriticalPercent),
			"paging_recovery_notifications":           fmt.Sprintf("%t", cfg.PagingRecoveryNotifications),
			"controller_recovery_enabled":             fmt.Sprintf("%t", cfg.ControllerRecoveryEnabled),
			"controller_recovery_include_ssh_key":     fmt.Sprintf("%t", cfg.ControllerRecoveryIncludeSSHKey),
			"controller_recovery_password_configured": fmt.Sprintf("%t", sec.ControllerRecoveryPassword != ""),
		},
	)
}
