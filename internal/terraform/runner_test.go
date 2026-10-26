package terraform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type runnerSink struct {
	mu       sync.Mutex
	messages []string
}

func (s *runnerSink) StageStarted(string)                     {}
func (s *runnerSink) StageComplete(string)                    {}
func (s *runnerSink) StageFailed(string, string, string, int) {}
func (s *runnerSink) Prompt(string, bool) (string, error)     { return "", nil }
func (s *runnerSink) Confirm(string) (bool, error)            { return true, nil }
func (s *runnerSink) ShowWarningBox([]string)                 {}
func (s *runnerSink) Log(_ string, _ string, message string, _ map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
}

func (s *runnerSink) contains(message string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(strings.Join(s.messages, "\n"), message)
}

func installTerraformHelper(t *testing.T, scenario string) {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "terraform")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec " + strconv.Quote(testBinary) + " -test.run=^TestTerraformRunnerHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(helper)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GO_WANT_TERRAFORM_HELPER", "1")
	t.Setenv("TERRAFORM_HELPER_SCENARIO", scenario)
}

func TestTerraformRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TERRAFORM_HELPER") != "1" {
		return
	}

	switch os.Getenv("TERRAFORM_HELPER_SCENARIO") {
	case "long-line":
		fmt.Fprintln(os.Stdout, strings.Repeat("a", 512*1024))
		fmt.Fprintln(os.Stdout, "after-long-line")
	case "oversized-line":
		fmt.Fprintln(os.Stdout, strings.Repeat("b", terraformLogLineLimit+32*1024))
		fmt.Fprintln(os.Stdout, "after-oversized-line")
	case "failure":
		fmt.Fprintln(os.Stderr, "intentional terraform failure")
		os.Exit(17)
	case "output-init":
		separator := -1
		for index, argument := range os.Args {
			if argument == "--" {
				separator = index
				break
			}
		}
		if separator < 0 {
			fmt.Fprintln(os.Stderr, "Terraform arguments are missing")
			os.Exit(2)
		}
		arguments := strings.Join(os.Args[separator+1:], " ")
		log, err := os.OpenFile(os.Getenv("TERRAFORM_HELPER_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprintf(log, "%s\t%s\n", arguments, os.Getenv("TF_DATA_DIR"))
		_ = log.Close()
		if strings.Contains(arguments, " -json ") {
			fmt.Fprintln(os.Stdout, `{"id":"project-123"}`)
		} else if strings.Contains(arguments, " output ") {
			fmt.Fprintln(os.Stdout, "project-123")
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown Terraform helper scenario")
		os.Exit(2)
	}
	os.Exit(0)
}

func TestOutputInitializesCurrentLockBeforeReadingProtectedLocalState(t *testing.T) {
	installTerraformHelper(t, "output-init")
	home := t.TempDir()
	t.Setenv("HOME", home)
	commandLog := filepath.Join(t.TempDir(), "terraform-commands.log")
	t.Setenv("TERRAFORM_HELPER_LOG", commandLog)
	module := t.TempDir()
	runner := &Runner{sink: &runnerSink{}}

	got, err := runner.Output(module, "bootstrap-gitlab", "iac_project_id")
	if err != nil {
		t.Fatal(err)
	}
	if got != "project-123" {
		t.Fatalf("Output() = %q, want project-123", got)
	}

	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(commands)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], " init -backend=false -reconfigure") || !strings.Contains(lines[1], " output -state=") {
		t.Fatalf("expected local lock initialization before protected-state output, commands were %q", commands)
	}
	if strings.Contains(lines[0], "backend-config") || strings.Contains(lines[1], "-state=remote") || strings.Contains(lines[1], "-state="+filepath.Join(home, ".local", "share", "bootstrap-home-infra", "terraform-state", ".terraform", "bootstrap-gitlab")) {
		t.Fatalf("output initialization must remain isolated from the authoritative backend: %q", commands)
	}
	cache := strings.SplitN(lines[0], "\t", 2)[1]
	if !strings.HasSuffix(cache, "bootstrap-gitlab-output") || strings.SplitN(lines[1], "\t", 2)[1] != cache {
		t.Fatalf("output must use one isolated provider cache, commands were %q", commands)
	}
}

func TestOutputJSONInitializesCurrentLockBeforeReadingProtectedLocalState(t *testing.T) {
	installTerraformHelper(t, "output-init")
	t.Setenv("HOME", t.TempDir())
	commandLog := filepath.Join(t.TempDir(), "terraform-commands.log")
	t.Setenv("TERRAFORM_HELPER_LOG", commandLog)
	module := t.TempDir()
	runner := &Runner{sink: &runnerSink{}}
	var got struct {
		ID string `json:"id"`
	}

	if err := runner.OutputJSON(module, "bootstrap-gitlab", "iac_project", &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "project-123" {
		t.Fatalf("OutputJSON() id = %q, want project-123", got.ID)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(commands)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], " init -backend=false -reconfigure") || !strings.Contains(lines[1], " output -state=") {
		t.Fatalf("expected local lock initialization before protected-state output, commands were %q", commands)
	}
	cache := strings.SplitN(lines[0], "\t", 2)[1]
	if !strings.HasSuffix(cache, "bootstrap-gitlab-output") || strings.SplitN(lines[1], "\t", 2)[1] != cache {
		t.Fatalf("output must use one isolated provider cache, commands were %q", commands)
	}
}

func TestRunnerRunDrainsLongTerraformPlanLine(t *testing.T) {
	installTerraformHelper(t, "long-line")
	sink := &runnerSink{}
	runner := &Runner{sink: sink}
	if err := runner.run(t.TempDir(), os.Environ(), "plan"); err != nil {
		t.Fatalf("run returned an error after draining a long plan line: %v", err)
	}
	if !sink.contains("after-long-line") {
		t.Fatal("runner did not drain output after a long Terraform plan line")
	}
}

func TestRunnerRunDrainsOversizedLineWithoutUnboundedLogging(t *testing.T) {
	installTerraformHelper(t, "oversized-line")
	sink := &runnerSink{}
	runner := &Runner{sink: sink}
	if err := runner.run(t.TempDir(), os.Environ(), "plan"); err != nil {
		t.Fatalf("run returned an error after draining an oversized plan line: %v", err)
	}
	if !sink.contains("truncated after") || !sink.contains("after-oversized-line") {
		t.Fatalf("oversized output was not truncated and fully drained: %q", sink.messages)
	}
}

func TestRunnerRunPropagatesTerraformExitFailure(t *testing.T) {
	installTerraformHelper(t, "failure")
	runner := &Runner{sink: &runnerSink{}}
	err := runner.run(t.TempDir(), os.Environ(), "plan")
	if err == nil {
		t.Fatal("expected Terraform helper failure")
	}
	if !strings.Contains(err.Error(), "terraform plan failed") || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatalf("unexpected Terraform failure: %v", err)
	}
}

func TestStreamTerraformOutputPropagatesReadError(t *testing.T) {
	want := errors.New("broken output pipe")
	err := streamTerraformOutput(errorReader{err: want}, &runnerSink{}, "info")
	if !errors.Is(err, want) {
		t.Fatalf("stream error = %v, want %v", err, want)
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

var _ io.Reader = errorReader{}

func TestDecodeStateMetadata(t *testing.T) {
	got, err := decodeStateMetadata([]byte(`{"lineage":"abc","serial":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Lineage != "abc" || got.Serial != 7 {
		t.Fatalf("unexpected metadata: %#v", got)
	}
	if _, err := decodeStateMetadata([]byte(`{"serial":1}`)); err == nil {
		t.Fatal("expected missing lineage to fail")
	}
}

func TestEquivalentStatePayloadAllowsOnlyWriterMetadataChanges(t *testing.T) {
	local := []byte(`{"lineage":"same","serial":7,"terraform_version":"1.0","resources":[{"type":"example","instances":[{"attributes":{"value":"secret"}}]}]}`)
	remote := []byte(`{"lineage":"same","serial":8,"terraform_version":"1.1","resources":[{"type":"example","instances":[{"attributes":{"value":"secret"}}]}]}`)
	equivalent, err := equivalentStatePayload(local, remote)
	if err != nil || !equivalent {
		t.Fatalf("writer metadata should not change payload equivalence: equivalent=%t err=%v", equivalent, err)
	}
	remote = []byte(`{"lineage":"same","serial":8,"terraform_version":"1.1","resources":[{"type":"example","instances":[{"attributes":{"value":"different"}}]}]}`)
	equivalent, err = equivalentStatePayload(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if equivalent {
		t.Fatal("resource payload drift was treated as equivalent")
	}
}

func TestTerraformEnvReplacesSensitiveInheritedValues(t *testing.T) {
	t.Setenv("TF_VAR_token", "old")
	t.Setenv("TF_DATA_DIR", "old-data")
	environment := terraformEnv("new-data", map[string]string{"token": "new"})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "TF_VAR_token=old") || strings.Contains(joined, "TF_DATA_DIR=old-data") {
		t.Fatalf("inherited Terraform value was not removed: %s", joined)
	}
	if !strings.Contains(joined, "TF_VAR_token=new") || !strings.Contains(joined, "TF_DATA_DIR=new-data") {
		t.Fatalf("replacement Terraform value missing: %s", joined)
	}
}

func TestApplySavedPlanUsesProtectedStatePath(t *testing.T) {
	args := applySavedPlanArgs("/protected/component.tfstate", "/logs/component.tfplan")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-state=/protected/component.tfstate") {
		t.Fatalf("apply arguments do not preserve the explicit state path: %v", args)
	}
	if args[len(args)-1] != "/logs/component.tfplan" {
		t.Fatalf("saved plan must remain the final argument: %v", args)
	}
}

func TestImportResourceArgsAreDeterministicAndUseProtectedState(t *testing.T) {
	args := importResourceArgs(
		"/protected/component.tfstate",
		"module.oidc.vault_jwt_auth_backend.oidc",
		"oidc",
		map[string]string{"z": "last", "a": "first"},
	)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-state=/protected/component.tfstate") {
		t.Fatalf("import arguments do not preserve the explicit state path: %v", args)
	}
	if strings.Index(joined, "a=first") > strings.Index(joined, "z=last") {
		t.Fatalf("import variables are not sorted: %v", args)
	}
	if got := args[len(args)-2:]; got[0] != "module.oidc.vault_jwt_auth_backend.oidc" || got[1] != "oidc" {
		t.Fatalf("resource address and ID must be final: %v", args)
	}
}

func TestLocalInitDiscardsStaleRemoteBackendMetadata(t *testing.T) {
	joined := strings.Join(localInitArgs(), " ")
	for _, required := range []string{"init", "-backend=false", "-reconfigure", "-input=false"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("local init does not include %s: %s", required, joined)
		}
	}
}

func TestMigrationInitSkipsTLSOnlyForLoopbackOrOptIn(t *testing.T) {
	local := strings.Join(migrationInitArgs("https://127.0.0.1:18443", false), " ")
	if !strings.Contains(local, "-backend-config=skip_cert_verification=true") {
		t.Fatalf("localhost migration did not enable the explicit test TLS override: %s", local)
	}
	production := strings.Join(migrationInitArgs("https://gitlab.home.example.com", false), " ")
	if strings.Contains(production, "skip_cert_verification") {
		t.Fatalf("production migration disabled TLS verification: %s", production)
	}
	opted := strings.Join(migrationInitArgs("https://gitlab.home.example.com", true), " ")
	if !strings.Contains(opted, "-backend-config=skip_cert_verification=true") {
		t.Fatalf("BOOTSTRAP_API_INSECURE did not reach the state backend: %s", opted)
	}
}

func TestStateHandoffPushesTheProtectedSourceSnapshot(t *testing.T) {
	args := statePushArgs("/protected/migration-source.tfstate")
	if got := strings.Join(args, " "); got != "state push -force /protected/migration-source.tfstate" {
		t.Fatalf("unexpected state push command: %s", got)
	}
}

func TestLocalTerraformMutationStopsAfterGitLabHandoff(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "dns.tfstate")
	if err := ensureLocalStateAuthority(statePath, "dns"); err != nil {
		t.Fatalf("state without a handoff should remain locally authoritative: %v", err)
	}
	recordPath := handoffRecordPath(statePath, "dns")
	if err := os.WriteFile(recordPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := ensureLocalStateAuthority(statePath, "dns")
	if err == nil {
		t.Fatal("expected local mutation to stop after GitLab handoff")
	}
	for _, required := range []string{"GitLab handoff", "GitLab pipeline", recordPath} {
		if !strings.Contains(err.Error(), required) {
			t.Fatalf("handoff error %q does not contain %q", err, required)
		}
	}
}

func TestAdoptLegacyState(t *testing.T) {
	moduleDir := t.TempDir()
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "component.tfstate")
	legacyPath := filepath.Join(moduleDir, "terraform.tfstate")
	state := []byte(`{"lineage":"legacy-lineage","serial":3}`)
	if err := os.WriteFile(legacyPath, state, 0644); err != nil {
		t.Fatal(err)
	}

	adopted, err := adoptLegacyState(moduleDir, statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !adopted {
		t.Fatal("expected legacy state to be adopted")
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(state) {
		t.Fatalf("adopted state changed: %s", got)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy state should be removed after adoption, got %v", err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("adopted state permissions = %o, want 600", info.Mode().Perm())
	}

	adopted, err = adoptLegacyState(moduleDir, statePath)
	if err != nil || adopted {
		t.Fatalf("existing protected state should win: adopted=%t err=%v", adopted, err)
	}
}

func TestAdoptLegacyStateRejectsInvalidState(t *testing.T) {
	moduleDir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "component.tfstate")
	if err := os.WriteFile(filepath.Join(moduleDir, "terraform.tfstate"), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := adoptLegacyState(moduleDir, statePath); err == nil {
		t.Fatal("expected invalid legacy state to be rejected")
	}
}

func TestProtectLocalState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "component.tfstate")
	for _, path := range []string{statePath, statePath + ".backup"} {
		if err := os.WriteFile(path, []byte("state"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := protectLocalState(statePath); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{statePath, statePath + ".backup"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions = %o, want 600", path, info.Mode().Perm())
		}
	}
	if err := protectLocalState(filepath.Join(t.TempDir(), "missing.tfstate")); err != nil {
		t.Fatalf("missing state should not fail protection: %v", err)
	}
}

func TestCopyTreeSkipsTerraformCache(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	if err := os.WriteFile(filepath.Join(source, "main.tf"), []byte("terraform {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, ".terraform"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".terraform", "cached"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.tf")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".terraform")); !os.IsNotExist(err) {
		t.Fatalf("expected .terraform cache to be skipped, got %v", err)
	}
}

func TestCopyTreePreservesStageRootModuleLayout(t *testing.T) {
	source := t.TempDir()
	root := filepath.Join(source, "terraform")
	stage := filepath.Join(root, "environments", "bootstrap", "dns")
	module := filepath.Join(root, "modules", "dns-records")
	for _, directory := range []string{stage, module} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "main.tf"), []byte("module \"dns\" { source = \"../../../modules/dns-records\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "main.tf"), []byte("terraform {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "terraform")
	if err := copyTree(root, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "environments", "bootstrap", "dns", "main.tf")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "modules", "dns-records", "main.tf")); err != nil {
		t.Fatal(err)
	}
}
