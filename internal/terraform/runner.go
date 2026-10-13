package terraform

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func handoffRecordPath(statePath, component string) string {
	return filepath.Join(filepath.Dir(statePath), component+".handoff.json")
}

func ensureLocalStateAuthority(statePath, component string) error {
	recordPath := handoffRecordPath(statePath, component)
	if _, err := os.Stat(recordPath); err == nil {
		return fmt.Errorf(
			"Terraform state %q has completed its GitLab handoff (%s); run day-2 changes through the GitLab pipeline instead of the local bootstrap runner",
			component,
			recordPath,
		)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking Terraform handoff record %s: %w", recordPath, err)
	}
	return nil
}

// ApplyModule creates a saved plan, obtains the accepted approval, and applies
// exactly that plan. Sensitive variables are environment-only.
func (r *Runner) ApplyModule(component, moduleDir string, variables, sensitive map[string]string) (string, error) {
	statePath, err := StatePath(component)
	if err != nil {
		return "", err
	}
	if err := ensureLocalStateAuthority(statePath, component); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return "", err
	}
	adopted, err := adoptLegacyState(moduleDir, statePath)
	if err != nil {
		return "", err
	}
	if adopted {
		r.sink.Log("warn", "terraform", "adopted Terraform state written beside the stage root by an earlier runner", map[string]string{
			"component": component,
			"state":     statePath,
		})
	}
	dataDir := filepath.Join(filepath.Dir(statePath), ".terraform", component)
	planPath := filepath.Join(r.rc.RunDir, component+".tfplan")
	environment := terraformEnv(dataDir, sensitive)

	if err := r.run(moduleDir, environment, localInitArgs()...); err != nil {
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
	if err := os.Chmod(planPath, 0600); err != nil {
		return "", fmt.Errorf("protecting saved Terraform plan %s: %w", planPath, err)
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
	applyErr := r.run(moduleDir, environment, applySavedPlanArgs(statePath, planPath)...)
	protectErr := protectLocalState(statePath)
	if applyErr != nil {
		return "", applyErr
	}
	if protectErr != nil {
		return "", protectErr
	}
	return statePath, nil
}

func protectLocalState(statePath string) error {
	for _, path := range []string{statePath, statePath + ".backup"} {
		if err := os.Chmod(path, 0600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("protecting Terraform state %s: %w", path, err)
		}
	}
	return nil
}

func applySavedPlanArgs(statePath, planPath string) []string {
	return []string{"apply", "-input=false", "-no-color", "-state=" + statePath, planPath}
}

// ImportIfMissing adopts a known durable API object after an interrupted
// provider create left the remote object present but no resource in local
// state. Import changes only controller state; the following saved plan still
// reconciles and approves every remote change.
func (r *Runner) ImportIfMissing(component, moduleDir, address, id string, variables, sensitive map[string]string) error {
	statePath, err := StatePath(component)
	if err != nil {
		return err
	}
	if err := ensureLocalStateAuthority(statePath, component); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return err
	}
	dataDir := filepath.Join(filepath.Dir(statePath), ".terraform", component)
	environment := terraformEnv(dataDir, sensitive)
	if err := r.run(moduleDir, environment, localInitArgs()...); err != nil {
		return err
	}
	if _, statErr := os.Stat(statePath); statErr == nil {
		resources, listErr := r.output(moduleDir, environment, "state", "list", "-state="+statePath)
		if listErr != nil {
			return listErr
		}
		for _, resource := range strings.Fields(string(resources)) {
			if resource == address {
				return nil
			}
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("checking Terraform state %s: %w", statePath, statErr)
	}

	r.sink.Log("warn", "terraform", "adopting an existing API object left by an interrupted Terraform create", map[string]string{
		"component": component,
		"resource":  address,
	})
	if err := r.run(moduleDir, environment, importResourceArgs(statePath, address, id, variables)...); err != nil {
		return err
	}
	return protectLocalState(statePath)
}

func importResourceArgs(statePath, address, id string, variables map[string]string) []string {
	args := []string{"import", "-input=false", "-no-color", "-state=" + statePath}
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-var", key+"="+variables[key])
	}
	return append(args, address, id)
}

func localInitArgs() []string {
	return []string{"init", "-backend=false", "-reconfigure", "-input=false", "-no-color"}
}

// adoptLegacyState recovers state produced by runner versions that supplied a
// custom state path to `terraform plan` but not to `terraform apply`. Terraform
// then wrote the applied state beside the root module. Validate it before
// moving it into the controller's protected state directory.
func adoptLegacyState(moduleDir, statePath string) (bool, error) {
	if _, err := os.Stat(statePath); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("checking Terraform state %s: %w", statePath, err)
	}

	legacyPath := filepath.Join(moduleDir, "terraform.tfstate")
	legacyState, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading legacy Terraform state %s: %w", legacyPath, err)
	}
	if _, err := decodeStateMetadata(legacyState); err != nil {
		return false, fmt.Errorf("refusing invalid legacy Terraform state %s: %w", legacyPath, err)
	}
	if err := os.Chmod(legacyPath, 0600); err != nil {
		return false, fmt.Errorf("protecting legacy Terraform state %s: %w", legacyPath, err)
	}
	if err := os.Rename(legacyPath, statePath); err != nil {
		return false, fmt.Errorf("moving legacy Terraform state to %s: %w", statePath, err)
	}
	return true, nil
}

func (r *Runner) Output(moduleDir, component, name string) (string, error) {
	statePath, err := StatePath(component)
	if err != nil {
		return "", err
	}
	// Output reads the protected local snapshot explicitly. Use a separate
	// provider cache so initializing current lock-file packages cannot disturb
	// backend metadata for the authoritative GitLab state.
	dataDir := outputDataDir(statePath, component)
	environment := terraformEnv(dataDir, nil)
	if err := r.run(moduleDir, environment, localInitArgs()...); err != nil {
		return "", fmt.Errorf("initializing Terraform before reading output %s: %w", name, err)
	}
	command := exec.Command("terraform", "-chdir="+moduleDir, "output", "-state="+statePath, "-raw", name) //nolint:gosec
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reading Terraform output %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// OutputJSON reads a structured Terraform output without converting a
// sensitive value to a command-line argument or temporary plaintext file.
func (r *Runner) OutputJSON(moduleDir, component, name string, destination any) error {
	statePath, err := StatePath(component)
	if err != nil {
		return err
	}
	dataDir := outputDataDir(statePath, component)
	environment := terraformEnv(dataDir, nil)
	if err := r.run(moduleDir, environment, localInitArgs()...); err != nil {
		return fmt.Errorf("initializing Terraform before reading output %s: %w", name, err)
	}
	command := exec.Command("terraform", "-chdir="+moduleDir, "output", "-state="+statePath, "-json", name) //nolint:gosec
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("reading Terraform output %s: %w", name, err)
	}
	if err := json.Unmarshal(output, destination); err != nil {
		return fmt.Errorf("decoding Terraform output %s: %w", name, err)
	}
	return nil
}

func outputDataDir(statePath, component string) string {
	return filepath.Join(filepath.Dir(statePath), ".terraform", component+"-output")
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
	if err := ensureLocalStateAuthority(statePath, component); err != nil {
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

	migrationRoot := filepath.Join(r.rc.RunDir, "terraform-migration", component)
	migrationDir := migrationRoot
	if strings.HasPrefix(filepath.Clean(moduleDir), "terraform"+string(filepath.Separator)) {
		if err := copyTree("terraform", filepath.Join(migrationRoot, "terraform")); err != nil {
			return fmt.Errorf("preparing Terraform migration tree: %w", err)
		}
		migrationDir = filepath.Join(migrationRoot, filepath.Clean(moduleDir))
	} else if err := copyTree(moduleDir, migrationDir); err != nil {
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
	migrationSourcePath := filepath.Join(migrationDir, "migration-source.tfstate")
	if err := os.WriteFile(migrationSourcePath, localState, 0600); err != nil {
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
	if err := r.run(migrationDir, environment, migrationInitArgs(gitlabURL)...); err != nil {
		return err
	}
	pushErr := r.run(migrationDir, environment, statePushArgs(migrationSourcePath)...)
	remoteState, err := r.output(migrationDir, environment, "state", "pull")
	if err != nil {
		return err
	}
	remoteMetadata, err := decodeStateMetadata(remoteState)
	if err != nil {
		return fmt.Errorf("validating migrated Terraform state: %w", err)
	}
	equivalent, err := equivalentStatePayload(localState, remoteState)
	if err != nil {
		return fmt.Errorf("comparing migrated Terraform state: %w", err)
	}
	if remoteMetadata.Lineage != localMetadata.Lineage || remoteMetadata.Serial < localMetadata.Serial || !equivalent {
		return fmt.Errorf(
			"migrated Terraform state mismatch: local lineage=%s serial=%d, remote lineage=%s serial=%d, equivalent=%t",
			localMetadata.Lineage,
			localMetadata.Serial,
			remoteMetadata.Lineage,
			remoteMetadata.Serial,
			equivalent,
		)
	}
	if pushErr != nil {
		r.sink.Log("warn", "terraform", "GitLab returned an error after persisting the verified state snapshot", map[string]string{
			"component": component,
			"error":     pushErr.Error(),
		})
	}

	record := map[string]any{
		"component":        component,
		"backend":          stateURL,
		"lineage":          localMetadata.Lineage,
		"serial":           remoteMetadata.Serial,
		"local_state_copy": statePath,
		"verified":         true,
	}
	recordBytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	recordBytes = append(recordBytes, '\n')
	recordPath := handoffRecordPath(statePath, component)
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

func migrationInitArgs(gitlabURL string) []string {
	args := []string{"init", "-input=false", "-no-color", "-migrate-state", "-force-copy"}
	if strings.Contains(gitlabURL, "127.0.0.1") || strings.Contains(gitlabURL, "localhost") {
		args = append(args, "-backend-config=skip_cert_verification=true")
	}
	return args
}

func statePushArgs(sourcePath string) []string {
	return []string{"state", "push", "-force", sourcePath}
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
	streamDone := make(chan error, 2)
	go func() { streamDone <- streamTerraformOutput(stdout, r.sink, "info") }()
	go func() { streamDone <- streamTerraformOutput(stderr, r.sink, "warn") }()

	// os/exec requires callers to finish reading StdoutPipe and StderrPipe
	// before Wait. The stream goroutines drain both pipes concurrently while
	// Terraform runs, including when an individual plan line is enormous.
	streamErrs := make([]error, 0, 2)
	for range 2 {
		streamErr := <-streamDone
		streamErrs = append(streamErrs, streamErr)
		if streamErr != nil {
			// A broken pipe reader can no longer guarantee that the child is
			// draining its output. Stop it so the other stream cannot wait on a
			// process blocked by the failed stream.
			_ = command.Process.Kill()
		}
	}
	waitErr := command.Wait()
	for _, streamErr := range streamErrs {
		if streamErr != nil {
			if waitErr != nil {
				streamErr = errors.Join(streamErr, waitErr)
			}
			return fmt.Errorf("reading terraform %s output: %w", args[0], streamErr)
		}
	}
	if waitErr != nil {
		return fmt.Errorf("terraform %s failed: %w", args[0], waitErr)
	}
	return nil
}

const terraformLogLineLimit = 256 * 1024

// streamTerraformOutput drains Terraform output without retaining unbounded
// plan lines. GitLab repository_file resources can render base64 source as one
// line. Stopping at a Scanner token limit leaves Terraform blocked writing to
// its pipe, so retain a useful prefix for the log and discard the remainder.
func streamTerraformOutput(reader io.Reader, sink stage.Sink, level string) error {
	buffered := bufio.NewReaderSize(reader, 32*1024)
	line := make([]byte, 0, terraformLogLineLimit)
	truncated := false

	emit := func() {
		message := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if truncated {
			message += fmt.Sprintf(" [truncated after %d bytes]", terraformLogLineLimit)
		}
		sink.Log(level, "terraform", message, nil)
	}

	for {
		fragment, err := buffered.ReadSlice('\n')
		if len(fragment) > 0 {
			remaining := terraformLogLineLimit - len(line)
			if remaining > 0 {
				if len(fragment) > remaining {
					line = append(line, fragment[:remaining]...)
					truncated = true
				} else {
					line = append(line, fragment...)
				}
			} else {
				truncated = true
			}
		}

		switch {
		case err == nil:
			emit()
			line = line[:0]
			truncated = false
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) > 0 || truncated {
				emit()
			}
			return nil
		default:
			if len(line) > 0 || truncated {
				emit()
			}
			return err
		}
	}
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

func equivalentStatePayload(localState, remoteState []byte) (bool, error) {
	var local, remote map[string]any
	if err := json.Unmarshal(localState, &local); err != nil {
		return false, fmt.Errorf("decoding local state: %w", err)
	}
	if err := json.Unmarshal(remoteState, &remote); err != nil {
		return false, fmt.Errorf("decoding remote state: %w", err)
	}
	for _, snapshot := range []map[string]any{local, remote} {
		delete(snapshot, "serial")
		delete(snapshot, "terraform_version")
	}
	return reflect.DeepEqual(local, remote), nil
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
			if info.Name() == ".terraform" {
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
