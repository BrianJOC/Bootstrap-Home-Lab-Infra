package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func testPublicKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}
	return signer.PublicKey()
}

func TestNormalizeTarget(t *testing.T) {
	tests := map[string]string{
		"server.home.arpa":      "server.home.arpa",
		"server.home.arpa:22":   "server.home.arpa",
		"server.home.arpa:2222": "server.home.arpa:2222",
		"[2001:db8::1]:2222":    "[2001:db8::1]:2222",
	}
	for input, want := range tests {
		if got := NormalizeTarget(input); got != want {
			t.Errorf("NormalizeTarget(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPinInspectAndReplaceHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "known_hosts")
	first := testPublicKey(t)
	replacement := testPublicKey(t)
	other := testPublicKey(t)

	state, _, err := InspectHostKey(path, "lab.home.arpa", first)
	if err != nil {
		t.Fatalf("inspecting unknown key: %v", err)
	}
	if state != TrustUnknown {
		t.Fatalf("unknown state = %v, want %v", state, TrustUnknown)
	}

	if err := PinHostKey(path, "lab.home.arpa", first); err != nil {
		t.Fatalf("pinning first key: %v", err)
	}
	if err := PinHostKey(path, "other.home.arpa:2222", other); err != nil {
		t.Fatalf("pinning unrelated key: %v", err)
	}

	state, oldFingerprint, err := InspectHostKey(path, "lab.home.arpa", first)
	if err != nil {
		t.Fatalf("inspecting matching key: %v", err)
	}
	if state != TrustMatch {
		t.Errorf("matching state = %v, want %v", state, TrustMatch)
	}
	if oldFingerprint != gossh.FingerprintSHA256(first) {
		t.Errorf("matching fingerprint = %q", oldFingerprint)
	}

	state, oldFingerprint, err = InspectHostKey(path, "lab.home.arpa", replacement)
	if err != nil {
		t.Fatalf("inspecting changed key: %v", err)
	}
	if state != TrustChanged {
		t.Errorf("changed state = %v, want %v", state, TrustChanged)
	}
	if oldFingerprint != gossh.FingerprintSHA256(first) {
		t.Errorf("old fingerprint = %q", oldFingerprint)
	}

	if err := PinHostKey(path, "lab.home.arpa", replacement); err != nil {
		t.Fatalf("replacing key: %v", err)
	}
	state, _, err = InspectHostKey(path, "lab.home.arpa", replacement)
	if err != nil || state != TrustMatch {
		t.Fatalf("replacement was not activated: state=%v err=%v", state, err)
	}
	state, _, err = InspectHostKey(path, "other.home.arpa:2222", other)
	if err != nil || state != TrustMatch {
		t.Fatalf("replacement altered unrelated pin: state=%v err=%v", state, err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading trust store: %v", err)
	}
	if strings.Count(string(content), "lab.home.arpa ") != 1 {
		t.Errorf("target pin count is not one:\n%s", content)
	}
	if strings.Contains(string(content), gossh.FingerprintSHA256(first)) {
		t.Error("trust store unexpectedly contains a fingerprint comment")
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat trust store: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("trust store mode = %04o, want 0600", fileInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat trust directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Errorf("trust directory mode = %04o, want 0700", dirInfo.Mode().Perm())
	}
}

func TestPinHostKeyPreservesMarkerEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	key := testPublicKey(t)
	entry := "@cert-authority *.home.arpa " + strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key))) + "\n"
	if err := os.WriteFile(path, []byte(entry), 0600); err != nil {
		t.Fatalf("writing marker entry: %v", err)
	}
	if err := PinHostKey(path, "lab.home.arpa", testPublicKey(t)); err != nil {
		t.Fatalf("pinning alongside marker: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading trust store: %v", err)
	}
	if !strings.Contains(string(content), "@cert-authority *.home.arpa") {
		t.Errorf("marker entry was not preserved:\n%s", content)
	}
}
