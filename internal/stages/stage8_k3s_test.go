package stages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/gitlab"
)

// withTokenFile points the GitLab client at a fixture PAT for the duration of a test.
func withTokenFile(t *testing.T, token string) {
	t.Helper()
	orig := gitlab.TokenFile
	t.Cleanup(func() { gitlab.TokenFile = orig })

	path := filepath.Join(t.TempDir(), "gitlab-api-token")
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		t.Fatalf("writing token fixture: %v", err)
	}
	gitlab.TokenFile = path
}

func TestClusterDefinitionsNoneWhenPathMissing(t *testing.T) {
	withTokenFile(t, "glpat-test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 invalid revision or path Not Found"}`))
	}))
	defer srv.Close()

	definitions, err := clusterDefinitions(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("clusterDefinitions() error = %v, want nil (missing path means no clusters)", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("clusterDefinitions() = %v, want none", definitions)
	}
}

func TestClusterDefinitionsListsYamlOnly(t *testing.T) {
	withTokenFile(t, "glpat-test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"name":"prod.yaml","path":"infrastructure/clusters/prod.yaml","type":"blob"},
			{"name":"notes.md","path":"infrastructure/clusters/notes.md","type":"blob"},
			{"name":"nested","path":"infrastructure/clusters/nested","type":"tree"}
		]`))
	}))
	defer srv.Close()

	definitions, err := clusterDefinitions(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("clusterDefinitions() error = %v", err)
	}
	if len(definitions) != 1 || definitions[0] != "prod.yaml" {
		t.Fatalf("clusterDefinitions() = %v, want [prod.yaml]", definitions)
	}
}

func TestClusterDefinitionsEmptyTree(t *testing.T) {
	withTokenFile(t, "glpat-test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	definitions, err := clusterDefinitions(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("clusterDefinitions() error = %v", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("clusterDefinitions() = %v, want none", definitions)
	}
}

func TestClusterDefinitionsFailsClosed(t *testing.T) {
	withTokenFile(t, "glpat-test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer srv.Close()

	if _, err := clusterDefinitions(context.Background(), srv.URL); err == nil {
		t.Fatal("clusterDefinitions() error = nil, want a hard error when the definition check cannot be completed")
	}
}

func TestClusterDefinitionsRequiresToken(t *testing.T) {
	orig := gitlab.TokenFile
	t.Cleanup(func() { gitlab.TokenFile = orig })
	gitlab.TokenFile = filepath.Join(t.TempDir(), "missing-token")

	if _, err := clusterDefinitions(context.Background(), "https://127.0.0.1:1"); err == nil {
		t.Fatal("clusterDefinitions() error = nil, want an error when the PAT is missing")
	}
}
