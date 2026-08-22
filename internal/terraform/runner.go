package terraform

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

type stateMetadata struct {
	Lineage string `json:"lineage"`
	Serial  int    `json:"serial"`
}

type Runner struct {
	sink stage.Sink
	rc   *runcontext.RunContext
}

func New(sink stage.Sink, rc *runcontext.RunContext) *Runner {
	return &Runner{sink: sink, rc: rc}
}

func StatePath(component string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(
		homeDir,
		".local",
		"share",
		"bootstrap-home-infra",
		"terraform-state",
		component+".tfstate",
	), nil
}

// ApplyModule creates a saved plan, obtains the accepted approval, and applies
// exactly that plan. Sensitive variables are environment-only.
func (r *Runner) ApplyModule(component, moduleDir string, variables, sensitive map[string]string) (string, error) {
	statePath, err := StatePath(component)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return "", err
	}
	dataDir := filepath.Join(filepath.Dir(statePath), ".terraform", component)
	planPath := filepath.Join(r.rc.RunDir, component+".tfplan")
	environment := terraformEnv(dataDir, sensitive)

	if err := r.run(moduleDir, environment, "init", "-backend=false", "-input=false", "-no-color"); err != nil {
		return "", err
	}
	args := []string{"plan", "-input=false", "-no-color", "-state=" + statePath, "-out=" + planPath}
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-var", key+"="+variables[key])
	}
	if err := r.run(moduleDir, environment, args...); err != nil {
		return "", err
	}

	if r.rc.Headless {
		if !r.rc.ApproveTerraform {
			return "", fmt.Errorf("saved Terraform plan %s requires --approve-terraform in headless mode", planPath)
		}
	} else {
		approved, confirmErr := r.sink.Confirm("Apply the saved Terraform plan at " + planPath + "?")
		if confirmErr != nil {
			return "", confirmErr
		}
		if !approved {
			return "", fmt.Errorf("Terraform plan was not approved")
		}
	}
	if err := r.run(moduleDir, environment, "apply", "-input=false", "-no-color", planPath); err != nil {
		return "", err
	}
	return statePath, nil
}

func (r *Runner) Output(moduleDir, component, name string) (string, error) {
	statePath, err := StatePath(component)
	if err != nil {
		return "", err
	}
	dataDir := filepath.Join(filepath.Dir(statePath), ".terraform", component)
	command := exec.Command("terraform", "-chdir="+moduleDir, "output", "-state="+statePath, "-raw", name) //nolint:gosec
	command.Env = terraformEnv(dataDir, nil)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reading Terraform output %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// MigrateStateToGitLab copies a locally authoritative state into GitLab's
// HTTP backend, verifies lineage and serial, and deliberately keeps the local
// state as a rollback copy. Credentials are supplied only through environment
// variables so they do not enter process arguments or generated files.
func (r *Runner) MigrateStateToGitLab(component, moduleDir, gitlabURL, projectID, token string) error {
	statePath, err := StatePath(component)
	if err != nil {
		return err
	}
	localState, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("reading local Terraform state: %w", err)
	}
	localMetadata, err := decodeStateMetadata(localState)
	if err != nil {
		return fmt.Errorf("validating local Terraform state: %w", err)
	}

	migrationDir := filepath.Join(r.rc.RunDir, "terraform-migration", component)
	if err := copyTree(moduleDir, migrationDir); err != nil {
		return fmt.Errorf("preparing Terraform migration root: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(migrationDir, "backend_override.tf"),
		[]byte("terraform {\n  backend \"http\" {}\n}\n"),
		0600,
	); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "terraform.tfstate"), localState, 0600); err != nil {
		return err
	}

	baseURL := strings.TrimRight(gitlabURL, "/")
	stateURL := fmt.Sprintf("%s/api/v4/projects/%s/terraform/state/%s", baseURL, projectID, component)
	dataDir := filepath.Join(migrationDir, ".terraform-data")
	environment := terraformEnv(dataDir, nil)
	environment = replaceEnv(environment, "TF_HTTP_USERNAME", "bootstrap")
	environment = replaceEnv(environment, "TF_HTTP_PASSWORD", token)
	environment = replaceEnv(environment, "TF_HTTP_ADDRESS", stateURL)
	environment = replaceEnv(environment, "TF_HTTP_LOCK_ADDRESS", stateURL+"/lock")
	environment = replaceEnv(environment, "TF_HTTP_UNLOCK_ADDRESS", stateURL+"/lock")
	environment = replaceEnv(environment, "TF_HTTP_LOCK_METHOD", "POST")
	environment = replaceEnv(environment, "TF_HTTP_UNLOCK_METHOD", "DELETE")
	environment = replaceEnv(environment, "TF_HTTP_RETRY_WAIT_MIN", "5")

	if err := r.run(
		migrationDir,
		environment,
		"init",
		"-input=false",
		"-no-color",
		"-migrate-state",
		"-force-copy",
	); err != nil {
		return err
	}
	remoteState, err := r.output(migrationDir, environment, "state", "pull")
	if err != nil {
		return err
	}
	remoteMetadata, err := decodeStateMetadata(remoteState)
	if err != nil {
		return fmt.Errorf("validating migrated Terraform state: %w", err)
	}
	if remoteMetadata != localMetadata {
		return fmt.Errorf(
			"migrated Terraform state identity mismatch: local lineage=%s serial=%d, remote lineage=%s serial=%d",
			localMetadata.Lineage,
			localMetadata.Serial,
			remoteMetadata.Lineage,
			remoteMetadata.Serial,
		)
	}

	record := map[string]any{
		"component":        component,
		"backend":          stateURL,
		"lineage":          localMetadata.Lineage,
		"serial":           localMetadata.Serial,
		"local_state_copy": statePath,
		"verified":         true,
	}
	recordBytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	recordBytes = append(recordBytes, '\n')
	recordPath := filepath.Join(filepath.Dir(statePath), component+".handoff.json")
	if err := os.WriteFile(recordPath+".tmp", recordBytes, 0600); err != nil {
		return err
	}
	if err := os.Rename(recordPath+".tmp", recordPath); err != nil {
		return err
	}
	r.sink.Log("info", "terraform", "GitLab state handoff verified; protected local rollback state retained", map[string]string{
		"component": component,
		"record":    recordPath,
	})
	return nil
}

func (r *Runner) run(moduleDir string, environment []string, args ...string) error {
	commandArgs := append([]string{"-chdir=" + moduleDir}, args...)
	command := exec.Command("terraform", commandArgs...) //nolint:gosec
	command.Env = environment
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("starting terraform %s: %w", args[0], err)
	}
	done := make(chan struct{}, 2)
	stream := func(scanner *bufio.Scanner, level string) {
		defer func() { done <- struct{}{} }()
		for scanner.Scan() {
			r.sink.Log(level, "terraform", scanner.Text(), nil)
		}
	}
	go stream(bufio.NewScanner(stdout), "info")
	go stream(bufio.NewScanner(stderr), "warn")
	waitErr := command.Wait()
	<-done
	<-done
	if waitErr != nil {
		return fmt.Errorf("terraform %s failed: %w", args[0], waitErr)
	}
	return nil
}

func (r *Runner) output(moduleDir string, environment []string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-chdir=" + moduleDir}, args...)
	command := exec.Command("terraform", commandArgs...) //nolint:gosec
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("terraform %s failed: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func terraformEnv(dataDir string, sensitive map[string]string) []string {
	blocked := map[string]struct{}{}
	for key := range sensitive {
		blocked["TF_VAR_"+key] = struct{}{}
	}
	blocked["TF_DATA_DIR"] = struct{}{}
	environment := make([]string, 0, len(os.Environ())+len(sensitive)+2)
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if _, skip := blocked[key]; !skip {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "TF_DATA_DIR="+dataDir, "TF_IN_AUTOMATION=1")
	for key, value := range sensitive {
		environment = append(environment, "TF_VAR_"+key+"="+value)
	}
	return environment
}

func replaceEnv(environment []string, key, value string) []string {
	prefix := key + "="
	filtered := environment[:0]
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func decodeStateMetadata(state []byte) (stateMetadata, error) {
	var metadata stateMetadata
	if err := json.Unmarshal(state, &metadata); err != nil {
		return stateMetadata{}, err
	}
	if metadata.Lineage == "" {
		return stateMetadata{}, fmt.Errorf("state has no lineage")
	}
	return metadata, nil
}

func copyTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			if relative == ".terraform" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
