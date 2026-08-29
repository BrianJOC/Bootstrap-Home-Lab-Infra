package terraform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestMigrationInitSkipsMockTLSOnlyForLocalhost(t *testing.T) {
	local := strings.Join(migrationInitArgs("https://127.0.0.1:18443"), " ")
	if !strings.Contains(local, "-backend-config=skip_cert_verification=true") {
		t.Fatalf("localhost migration did not enable the explicit test TLS override: %s", local)
	}
	production := strings.Join(migrationInitArgs("https://gitlab.home.example.com"), " ")
	if strings.Contains(production, "skip_cert_verification") {
		t.Fatalf("production migration disabled TLS verification: %s", production)
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
