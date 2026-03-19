package ansible

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
// inventory, and extra vars. Extra var values are never logged.
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
	r.sink.Info("ansible", fmt.Sprintf("running playbook %s with vars: %s", playbookPath, strings.Join(keys, ", ")))

	args := []string{
		playbookPath,
		"-i", inventory,
		"--extra-vars", extraVarsJSON,
	}

	cmd := exec.Command("ansible-playbook", args...) //nolint:gosec
	cmd.Env = os.Environ()

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
			r.sink.Info("ansible", scanner.Text())
		}
	}()

	// Stream stderr line by line to the sink.
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			r.sink.Warn("ansible", scanner.Text())
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
