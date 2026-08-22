package stages

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

const minUbuntuVersion = "22.04"

// ConfigStage is Stage 0: config collection and SSH validation.
type ConfigStage struct{}

// Name returns the canonical stage identifier.
func (s *ConfigStage) Name() string { return "config-collection" }

// Check always returns false — config collection always runs to validate inputs.
func (s *ConfigStage) Check(_ context.Context, _ *config.Config, _ *runcontext.RunContext) (bool, error) {
	return false, nil
}

// Run executes Stage 0: validates config, establishes SSH, detects arch/OS, writes files.
func (s *ConfigStage) Run(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	sink.Log("info", s.Name(), "validating configuration", nil)

	if err := config.Validate(&config.RunConfig{Config: cfg, Secrets: sec}); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// SSH connectivity with re-prompt loop on auth failure (B4).
	client, err := s.dialWithRetry(ctx, cfg, rc, sink)
	if err != nil {
		return err
	}
	defer client.Close()

	// Detect target architecture.
	sink.Log("info", s.Name(), "detecting target architecture", nil)
	archOut, err := client.DetectArch()
	if err != nil {
		return fmt.Errorf("arch detection failed: %w", err)
	}
	arch := config.ParseArch(strings.TrimSpace(archOut))
	if arch == config.ArchUnknown {
		ok, verifyErr := sink.Confirm(fmt.Sprintf("unknown architecture %q detected — continue?", strings.TrimSpace(archOut)))
		if verifyErr != nil {
			return fmt.Errorf("confirm failed: %w", verifyErr)
		}
		if !ok {
			return fmt.Errorf("aborted: unknown architecture")
		}
	}
	rc.TargetArch = arch
	sink.Log("info", s.Name(), fmt.Sprintf("detected arch: %s", arch), nil)

	// Detect and validate target OS (B3).
	sink.Log("info", s.Name(), "detecting target OS", nil)
	if err := s.checkOS(client, sink); err != nil {
		return err
	}

	// Write run manifest with real SHA-256 hash (B2).
	sink.Log("info", s.Name(), "writing run manifest", nil)
	if err := writeManifest(cfg, rc); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}

	sink.Log("info", s.Name(), "stage complete", nil)
	return nil
}

// dialWithRetry attempts SSH connection, re-prompting on auth failure (B4).
func (s *ConfigStage) dialWithRetry(ctx context.Context, cfg *config.Config, rc *runcontext.RunContext, sink stage.Sink) (*ssh.Client, error) {
	keyPath := cfg.SSHKeyPath
	host := cfg.TargetHost
	user := cfg.SSHUser

	if err := s.ensureHostTrust(cfg, rc, sink); err != nil {
		return nil, err
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sink.Log("info", s.Name(), fmt.Sprintf("testing SSH connectivity to %s (attempt %d)", host, attempt), nil)
		client, err := ssh.New(host, user, keyPath, rc.KnownHostsPath)
		if err == nil {
			return client, nil
		}

		switch {
		case errors.Is(err, ssh.ErrAuthFailed):
			sink.Log("warn", s.Name(), fmt.Sprintf("SSH auth failed: %v", err), nil)
			if attempt == 3 {
				return nil, fmt.Errorf("SSH authentication failed after 3 attempts")
			}
			newPath, promptErr := sink.Prompt("SSH key path (auth failed, enter correct path)", false)
			if promptErr != nil {
				return nil, fmt.Errorf("prompt failed: %w", promptErr)
			}
			keyPath = strings.TrimSpace(newPath)
			// Persist the corrected key path so subsequent stages use it.
			cfg.SSHKeyPath = keyPath

		case errors.Is(err, ssh.ErrConnRefused):
			sink.Log("warn", s.Name(), fmt.Sprintf("SSH connection refused: %v", err), nil)
			ok, confirmErr := sink.Confirm(fmt.Sprintf("Connection refused to %s — retry?", host))
			if confirmErr != nil || !ok {
				return nil, fmt.Errorf("SSH connection refused: %w", err)
			}

		case errors.Is(err, ssh.ErrConnTimeout):
			sink.Log("warn", s.Name(), fmt.Sprintf("SSH connection timed out: %v", err), nil)
			ok, confirmErr := sink.Confirm(fmt.Sprintf("Connection timed out to %s — retry?", host))
			if confirmErr != nil || !ok {
				return nil, fmt.Errorf("SSH connection timed out: %w", err)
			}

		default:
			return nil, fmt.Errorf("SSH connectivity check failed: %w", err)
		}
	}
	return nil, fmt.Errorf("SSH connectivity failed after retries")
}

func (s *ConfigStage) ensureHostTrust(cfg *config.Config, rc *runcontext.RunContext, sink stage.Sink) error {
	sink.Log("info", s.Name(), "observing SSH host identity before authentication", map[string]string{"target": cfg.TargetHost})
	info, err := ssh.ObserveHostKey(cfg.TargetHost)
	if err != nil {
		return err
	}
	state, oldFingerprint, err := ssh.InspectHostKey(rc.KnownHostsPath, cfg.TargetHost, info.Key)
	if err != nil {
		return err
	}
	if cfg.SSHHostFingerprint != "" && cfg.SSHHostFingerprint != info.Fingerprint {
		return fmt.Errorf("observed SSH fingerprint %s does not match configured ssh_host_fingerprint %s", info.Fingerprint, cfg.SSHHostFingerprint)
	}

	details := fmt.Sprintf("target=%s address=%s algorithm=%s fingerprint=%s",
		info.Target, info.Address, info.Algorithm, info.Fingerprint)

	switch state {
	case ssh.TrustMatch:
		sink.Log("info", s.Name(), "SSH host identity matches the pinned key", map[string]string{"fingerprint": info.Fingerprint})
		return nil

	case ssh.TrustUnknown:
		if rc.Headless {
			if cfg.SSHHostFingerprint == "" && !rc.AcceptNewHostKey {
				return fmt.Errorf("SSH host is unknown; headless first use requires ssh_host_fingerprint or --accept-new-host-key")
			}
		} else {
			accepted, confirmErr := sink.Confirm("Unknown SSH host. Confirm this fingerprint and pin it? " + details)
			if confirmErr != nil {
				return fmt.Errorf("confirming SSH host identity: %w", confirmErr)
			}
			if !accepted {
				return fmt.Errorf("SSH host fingerprint was not accepted")
			}
		}
		if err := ssh.PinHostKey(rc.KnownHostsPath, cfg.TargetHost, info.Key); err != nil {
			return err
		}
		sink.Log("info", s.Name(), "pinned new SSH host identity", map[string]string{"fingerprint": info.Fingerprint})
		return nil

	case ssh.TrustChanged:
		if rc.Headless {
			if !rc.ReplaceHostKey || cfg.SSHHostFingerprint == "" {
				return fmt.Errorf("SSH host key changed from %s to %s; headless replacement requires --replace-host-key and the expected ssh_host_fingerprint", oldFingerprint, info.Fingerprint)
			}
		} else {
			accepted, confirmErr := sink.Confirm(fmt.Sprintf(
				"SSH HOST KEY CHANGED from %s to %s. Did you intentionally reinstall or replace %s?",
				oldFingerprint, info.Fingerprint, cfg.TargetHost,
			))
			if confirmErr != nil {
				return fmt.Errorf("confirming changed SSH host identity: %w", confirmErr)
			}
			if !accepted {
				return fmt.Errorf("changed SSH host key was not accepted")
			}
			typedTarget, promptErr := sink.Prompt("Type the target exactly to replace its pinned SSH key: "+cfg.TargetHost, false)
			if promptErr != nil {
				return fmt.Errorf("confirming target for SSH key replacement: %w", promptErr)
			}
			if strings.TrimSpace(typedTarget) != cfg.TargetHost {
				return fmt.Errorf("target confirmation did not match; pinned SSH key was not changed")
			}
		}
		if err := ssh.PinHostKey(rc.KnownHostsPath, cfg.TargetHost, info.Key); err != nil {
			return err
		}
		sink.Log("warn", s.Name(), "replaced changed SSH host identity", map[string]string{
			"old_fingerprint": oldFingerprint,
			"new_fingerprint": info.Fingerprint,
		})
		return nil
	default:
		return fmt.Errorf("unsupported SSH trust state")
	}
}

// checkOS validates the target is Ubuntu >= 22.04 (B3).
func (s *ConfigStage) checkOS(client *ssh.Client, sink stage.Sink) error {
	osName, osVersion, err := client.DetectOS()
	if err != nil {
		sink.Log("warn", s.Name(), fmt.Sprintf("could not detect OS: %v", err), nil)
		return nil
	}
	osName = strings.TrimSpace(osName)
	osVersion = strings.TrimSpace(osVersion)
	sink.Log("info", s.Name(), fmt.Sprintf("detected OS: %s %s", osName, osVersion), nil)

	if osName != "Ubuntu" {
		ok, err := sink.Confirm(fmt.Sprintf("target OS is %q, not Ubuntu — continue?", osName))
		if err != nil {
			return fmt.Errorf("confirm failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("aborted: unsupported OS %q", osName)
		}
		return nil
	}

	// Check Ubuntu version >= 22.04.
	if !versionAtLeast(osVersion, minUbuntuVersion) {
		ok, err := sink.Confirm(fmt.Sprintf("Ubuntu %s detected; minimum supported is %s — continue anyway?", osVersion, minUbuntuVersion))
		if err != nil {
			return fmt.Errorf("confirm failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("aborted: Ubuntu %s is below minimum %s", osVersion, minUbuntuVersion)
		}
	}
	return nil
}

// versionAtLeast returns true if ver >= min (semver-style, e.g. "22.04" >= "22.04").
func versionAtLeast(ver, min string) bool {
	vParts := strings.SplitN(ver, ".", 3)
	mParts := strings.SplitN(min, ".", 3)
	// Pad to same length.
	for len(vParts) < len(mParts) {
		vParts = append(vParts, "0")
	}
	for i, mp := range mParts {
		vNum, _ := strconv.Atoi(vParts[i])
		mNum, _ := strconv.Atoi(mp)
		if vNum != mNum {
			return vNum > mNum
		}
	}
	return true // equal
}

// manifest is written to the run directory as manifest.json.
type manifest struct {
	TargetHost string    `json:"target_host"`
	Arch       string    `json:"arch"`
	StartTime  time.Time `json:"start_time"`
	ConfigHash string    `json:"config_hash"`
	PID        int       `json:"pid"`
}

// writeManifest writes the run manifest with a real SHA-256 config hash (B2 fix).
func writeManifest(cfg *config.Config, rc *runcontext.RunContext) error {
	logDir := rc.RunDir
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return fmt.Errorf("creating run dir: %w", err)
	}

	m := manifest{
		TargetHost: cfg.TargetHost,
		Arch:       string(rc.TargetArch),
		StartTime:  rc.StartTime,
		ConfigHash: configHash(cfg),
		PID:        os.Getpid(),
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}

	return os.WriteFile(filepath.Join(logDir, "manifest.json"), data, 0600)
}

// configHash computes a SHA-256 hash of the marshalled config YAML (B2 fix).
func configHash(cfg *config.Config) string {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "hash-error"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
