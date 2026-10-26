package ansible

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// Runner invokes ansible-playbook using the SSH connection for inventory.
type Runner struct {
	sshClient *ssh.Client
	sink      stage.Sink
}

// New creates a new Ansible Runner.
func New(client *ssh.Client, sink stage.Sink) *Runner {
	return &Runner{sshClient: client, sink: sink}
}

// RunPlaybook runs an ansible-playbook command with the given playbook path,
// inventory host:port, and extra vars. Extra var values are never logged.
func (r *Runner) RunPlaybook(playbookPath, inventory string, extraVars map[string]string) error {
	// Build the --extra-vars JSON. Values are never logged; only keys are.
	extraVarsJSON, err := buildExtraVarsJSON(extraVars)
	if err != nil {
		return fmt.Errorf("marshalling extra vars: %w", err)
	}

	// Log only the keys, never the values.
	keys := make([]string, 0, len(extraVars))
	for k := range extraVars {
		keys = append(keys, k)
	}
	r.sink.Log("info", "ansible", fmt.Sprintf("running playbook %s with vars: %s", playbookPath, strings.Join(keys, ", ")), nil)

	// Write a temp INI inventory so the [bootstrap_target] group is defined.
	invFile, err := buildInventoryFile(
		strings.TrimSuffix(inventory, ","),
		r.sshClient.User(),
		r.sshClient.KeyPath(),
	)
	if err != nil {
		return fmt.Errorf("building inventory: %w", err)
	}
	defer os.Remove(invFile)

	// Write extra-vars to a temp file (mode 0600) to keep secret values off the
	// process command line (which is visible in /proc/<pid>/cmdline).
	evFile, err := os.CreateTemp("", "bootstrap-extravars-*.json")
	if err != nil {
		return fmt.Errorf("creating extra-vars temp file: %w", err)
	}
	evPath := evFile.Name()
	defer os.Remove(evPath)
	if err := os.Chmod(evPath, 0600); err != nil {
		evFile.Close()
		return fmt.Errorf("restricting extra-vars file permissions: %w", err)
	}
	if _, err := evFile.WriteString(extraVarsJSON); err != nil {
		evFile.Close()
		return fmt.Errorf("writing extra-vars file: %w", err)
	}
	evFile.Close()

	args := []string{
		playbookPath,
		"-i", invFile,
		"--extra-vars", "@" + evPath,
	}

	cmd := exec.Command("ansible-playbook", args...) //nolint:gosec
	cmd.Env = safeEnv()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ansible-playbook: %w", err)
	}

	// Stream stdout line by line to the sink.
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			r.sink.Log("info", "ansible", scanner.Text(), nil)
		}
	}()

	// Stream stderr line by line to the sink.
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			r.sink.Log("warn", "ansible", scanner.Text(), nil)
		}
	}()

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("ansible-playbook exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("ansible-playbook failed: %w", err)
	}

	return nil
}

// secretEnvKeys is the set of bootstrap secret env vars that must never be
// forwarded to ansible-playbook processes. Secrets are passed explicitly via
// --extra-vars JSON, never via the process environment.
// IMPORTANT: Any new secret added to config.Secrets that may be present in
// the process environment must also be listed here to prevent leakage.
var secretEnvKeys = map[string]struct{}{
	"CF_DNS_API_TOKEN":         {},
	"CLOUDFLARE_DNS_API_TOKEN": {},
	"BOOTSTRAP_ADMIN_PASSWORD": {},
}

// safeEnv returns os.Environ() with all known bootstrap secret keys removed.
func safeEnv() []string {
	raw := os.Environ()
	filtered := make([]string, 0, len(raw))
	for _, kv := range raw {
		key := kv
		if idx := strings.Index(kv, "="); idx >= 0 {
			key = kv[:idx]
		}
		if _, blocked := secretEnvKeys[key]; !blocked {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

// buildInventoryFile writes a temporary INI inventory file that defines the
// [bootstrap_target] group expected by all stage playbooks. The hostPort
// argument is "host:port" or bare "host" (port defaults to 22). The explicit
// SSH user and key ensure Ansible uses the identity already validated by the
// stage's SSH preflight instead of repository test defaults.
// The caller must remove the returned file when done.
func buildInventoryFile(hostPort, user, keyPath string) (string, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		port = "22"
	}
	content := fmt.Sprintf(
		"[bootstrap_target]\n%s ansible_port=%s ansible_user=%q ansible_ssh_private_key_file=%q\n",
		host,
		port,
		user,
		keyPath,
	)
	f, err := os.CreateTemp("", "bootstrap-inventory-*.ini")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(f.Name(), 0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// buildExtraVarsJSON marshals extra vars into a JSON string for --extra-vars.
func buildExtraVarsJSON(vars map[string]string) (string, error) {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(vars); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
