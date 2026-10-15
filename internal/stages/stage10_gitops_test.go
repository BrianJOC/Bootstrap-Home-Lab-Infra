package stages

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeRails(t *testing.T, directory string) (string, string) {
	t.Helper()
	installFlockShimIfMissing(t, directory)
	state := filepath.Join(directory, "fake-rails-state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	rails := filepath.Join(directory, "gitlab-rails")
	contents := `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_RAILS_STATE/argv"
if [[ "$1" != runner ]]; then exit 40; fi
case "$2" in
  *find_by_token*)
    IFS= read -r supplied
    expected=$(cat "$FAKE_RAILS_STATE/active-token")
    if [[ "$supplied" != "$expected" ]]; then printf '%s' "$supplied" >&2; exit 41; fi
    printf '%s' "$expected"
    ;;
  *personal_access_tokens.create!*)
    printf x >>"$FAKE_RAILS_STATE/mints"
    cat "$FAKE_RAILS_STATE/new-token"
    ;;
  *) exit 42 ;;
esac
`
	if err := os.WriteFile(rails, []byte(contents), 0700); err != nil {
		t.Fatal(err)
	}
	return state, rails
}

func installFlockShimIfMissing(t *testing.T, directory string) {
	t.Helper()
	if _, err := exec.LookPath("flock"); err == nil {
		return
	}
	bin := filepath.Join(directory, "test-bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(bin, "flock")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runDay2CredentialScript(t *testing.T, directory, rails string) ([]byte, error) {
	t.Helper()
	script := day2CredentialScript(directory, rails)
	command := exec.Command("/bin/bash", "-c", script)
	return command.CombinedOutput()
}

func assertCredentialAbsentFromRailsArguments(t *testing.T, state, credential string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), credential) {
		t.Fatal("credential appeared in the Rails argument log")
	}
}

func TestDay2CredentialScriptCreatesPrivateTokenFile(t *testing.T) {
	directory := t.TempDir()
	state, rails := writeFakeRails(t, directory)
	t.Setenv("FAKE_RAILS_STATE", state)
	const token = "glpat-createdToken_123.01.fixture"
	if strings.Contains(day2CredentialScript(directory, rails), token) {
		t.Fatal("credential appeared in the generated shell command")
	}
	if err := os.WriteFile(filepath.Join(state, "new-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}

	output, err := runDay2CredentialScript(t, directory, rails)
	if err != nil {
		t.Fatalf("credential creation failed: %v", err)
	}
	if string(output) != token+"\n" {
		t.Fatal("credential script did not return exactly the created token")
	}
	for name, want := range map[string]string{
		"bootstrap-day2-iac.token": token,
		"bootstrap-day2-iac.lock":  "",
	} {
		path := filepath.Join(directory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("unexpected contents in %s", name)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %04o, want 0600", name, info.Mode().Perm())
		}
	}
	if strings.Contains(string(output), "error") {
		t.Fatal("successful command returned diagnostic text with the credential")
	}
	assertCredentialAbsentFromRailsArguments(t, state, token)
}

func TestDay2CredentialScriptReusesActiveTokenWithoutMinting(t *testing.T) {
	directory := t.TempDir()
	state, rails := writeFakeRails(t, directory)
	t.Setenv("FAKE_RAILS_STATE", state)
	const token = "glpat-existingToken_456.01.fixture"
	if err := os.WriteFile(filepath.Join(directory, "bootstrap-day2-iac.token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "active-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}

	output, err := runDay2CredentialScript(t, directory, rails)
	if err != nil {
		t.Fatalf("active credential reuse failed: %v", err)
	}
	if string(output) != token+"\n" {
		t.Fatal("credential script did not return the existing token")
	}
	if _, err := os.Stat(filepath.Join(state, "mints")); !os.IsNotExist(err) {
		t.Fatal("active token reuse unexpectedly minted another token")
	}
	data, err := os.ReadFile(filepath.Join(directory, "bootstrap-day2-iac.token"))
	if err != nil || string(data) != token {
		t.Fatal("active token file changed during reuse")
	}
	assertCredentialAbsentFromRailsArguments(t, state, token)
}

func TestDay2CredentialScriptLeavesInactiveTokenUntouched(t *testing.T) {
	directory := t.TempDir()
	state, rails := writeFakeRails(t, directory)
	t.Setenv("FAKE_RAILS_STATE", state)
	const token = "glpat-inactiveToken_789"
	if err := os.WriteFile(filepath.Join(directory, "bootstrap-day2-iac.token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "active-token"), []byte("glpat-anotherActiveToken"), 0600); err != nil {
		t.Fatal(err)
	}

	output, err := runDay2CredentialScript(t, directory, rails)
	if err == nil {
		t.Fatal("inactive token validation unexpectedly succeeded")
	}
	if strings.Contains(string(output), token) {
		t.Fatal("failed command exposed the token in its output")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("failed command exposed the token in its error")
	}
	data, readErr := os.ReadFile(filepath.Join(directory, "bootstrap-day2-iac.token"))
	if readErr != nil || string(data) != token {
		t.Fatal("inactive token file was changed")
	}
	if _, statErr := os.Stat(filepath.Join(state, "mints")); !os.IsNotExist(statErr) {
		t.Fatal("inactive token caused automatic minting")
	}
	assertCredentialAbsentFromRailsArguments(t, state, token)
}

func TestDay2CredentialScriptRejectsNonPrivateExistingToken(t *testing.T) {
	directory := t.TempDir()
	state, rails := writeFakeRails(t, directory)
	t.Setenv("FAKE_RAILS_STATE", state)
	const token = "glpat-tooOpenToken_456"
	tokenPath := filepath.Join(directory, "bootstrap-day2-iac.token")
	if err := os.WriteFile(tokenPath, []byte(token), 0644); err != nil {
		t.Fatal(err)
	}
	// The release runner uses umask 077; establish the deliberately unsafe
	// fixture mode explicitly rather than letting that mask make it private.
	if err := os.Chmod(tokenPath, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "active-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}

	output, err := runDay2CredentialScript(t, directory, rails)
	if err == nil {
		t.Fatal("non-private token file unexpectedly passed validation")
	}
	if strings.Contains(string(output), token) || strings.Contains(err.Error(), token) {
		t.Fatal("failed command exposed the token")
	}
	data, readErr := os.ReadFile(tokenPath)
	if readErr != nil || string(data) != token {
		t.Fatal("non-private token file was changed")
	}
	if _, statErr := os.Stat(filepath.Join(state, "argv")); !os.IsNotExist(statErr) {
		t.Fatal("Rails was called before private-file ownership and mode checks")
	}
}
