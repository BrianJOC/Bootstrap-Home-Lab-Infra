package ansible

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ── buildInventoryFile ────────────────────────────────────────────────────────

func TestBuildInventoryFile_HostPort(t *testing.T) {
	path, err := buildInventoryFile("192.168.1.10:2222", "bootstrap", "/tmp/id_ed25519")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading inventory file: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "[bootstrap_target]") {
		t.Errorf("inventory missing [bootstrap_target] group:\n%s", got)
	}
	if !strings.Contains(got, "192.168.1.10") {
		t.Errorf("inventory missing host 192.168.1.10:\n%s", got)
	}
	if !strings.Contains(got, "ansible_port=2222") {
		t.Errorf("inventory missing ansible_port=2222:\n%s", got)
	}
	if !strings.Contains(got, `ansible_user="bootstrap"`) {
		t.Errorf("inventory missing SSH user:\n%s", got)
	}
	if !strings.Contains(got, `ansible_ssh_private_key_file="/tmp/id_ed25519"`) {
		t.Errorf("inventory missing SSH key path:\n%s", got)
	}
}

func TestBuildInventoryFile_HostOnly(t *testing.T) {
	path, err := buildInventoryFile("10.0.0.5", "bootstrap", "/tmp/id_ed25519")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading inventory file: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "ansible_port=22") {
		t.Errorf("expected default port 22; got:\n%s", got)
	}
}

func TestBuildInventoryFile_PermissionsAre0600(t *testing.T) {
	path, err := buildInventoryFile("10.0.0.1", "bootstrap", "/tmp/id_ed25519")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file permissions: got %04o, want 0600", info.Mode().Perm())
	}
}

// ── buildExtraVarsJSON ────────────────────────────────────────────────────────

func TestBuildExtraVarsJSON_BasicMap(t *testing.T) {
	vars := map[string]string{
		"internal_domain":     "home.lab",
		"bootstrap_server_ip": "192.168.1.1",
	}
	out, err := buildExtraVarsJSON(vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must be valid JSON.
	var decoded map[string]string
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	for k, v := range vars {
		if decoded[k] != v {
			t.Errorf("key %q: got %q, want %q", k, decoded[k], v)
		}
	}
}

func TestBuildExtraVarsJSON_EmptyMap(t *testing.T) {
	out, err := buildExtraVarsJSON(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "{}" {
		t.Errorf("expected \"{}\", got %q", out)
	}
}

func TestBuildExtraVarsJSON_NoHTMLEscaping(t *testing.T) {
	// Values containing < > & must not be HTML-escaped (would break Ansible vars).
	vars := map[string]string{"url": "https://example.com/path?a=1&b=2"}
	out, err := buildExtraVarsJSON(vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `\u003c`) || strings.Contains(out, `\u0026`) {
		t.Errorf("output contains HTML-escaped characters: %s", out)
	}
}

// ── safeEnv ───────────────────────────────────────────────────────────────────

func TestSafeEnv_FiltersSecretKeys(t *testing.T) {
	for key := range secretEnvKeys {
		t.Setenv(key, "must-not-appear")
	}

	env := safeEnv()
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		key := parts[0]
		if _, blocked := secretEnvKeys[key]; blocked {
			t.Errorf("safeEnv() returned secret key %q (value must not be forwarded to subprocesses)", key)
		}
	}
}

func TestSafeEnv_KeepsNonSecretVars(t *testing.T) {
	t.Setenv("BOOTSTRAP_TEST_MARKER_SAFE", "present")
	// Ensure a secret key is set so filtering is active.
	t.Setenv("CF_DNS_API_TOKEN", "secret-cf")

	env := safeEnv()
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "BOOTSTRAP_TEST_MARKER_SAFE=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("safeEnv() removed a non-secret env var")
	}
}

func TestSafeEnv_EnvEntryWithoutEquals(t *testing.T) {
	// safeEnv must not panic if an env entry has no '=' separator.
	// We can't inject such an entry via os.Setenv, but we can verify the
	// function completes successfully with a normal environment.
	env := safeEnv()
	if env == nil {
		t.Error("safeEnv() returned nil")
	}
}
