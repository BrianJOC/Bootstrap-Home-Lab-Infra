package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListTreeParsesEntries(t *testing.T) {
	var gotPath, gotRef, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		gotRef = r.URL.Query().Get("ref")
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"prod.yaml","path":"infrastructure/clusters/prod.yaml","type":"blob"}]`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "glpat-test")
	entries, err := client.ListTree(context.Background(), "infrastructure/iac", "infrastructure/clusters", "main")
	if err != nil {
		t.Fatalf("ListTree() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "prod.yaml" || entries[0].Type != "blob" {
		t.Fatalf("ListTree() = %+v, want one prod.yaml blob", entries)
	}
	if gotPath != "infrastructure/clusters" || gotRef != "main" {
		t.Errorf("query params path=%q ref=%q, want infrastructure/clusters/main", gotPath, gotRef)
	}
	if gotToken != "glpat-test" {
		t.Errorf("PRIVATE-TOKEN header = %q, want the client token", gotToken)
	}
}

func TestListTreeEmptyTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	entries, err := NewClient(srv.URL, "t").ListTree(context.Background(), "infrastructure/iac", "infrastructure/clusters", "main")
	if err != nil {
		t.Fatalf("ListTree() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListTree() = %+v, want empty", entries)
	}
}

func TestListTreeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 invalid revision or path Not Found"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "t").ListTree(context.Background(), "infrastructure/iac", "infrastructure/clusters", "main")
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("ListTree() error = %v, want ErrPathNotFound", err)
	}
}

func TestListTreeAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "bad").ListTree(context.Background(), "infrastructure/iac", "infrastructure/clusters", "main")
	if err == nil {
		t.Fatal("ListTree() error = nil, want an error for 401")
	}
	if errors.Is(err, ErrPathNotFound) {
		t.Fatalf("401 must not be treated as a missing path: %v", err)
	}
}

func TestTokenFromHome(t *testing.T) {
	orig := TokenFile
	t.Cleanup(func() { TokenFile = orig })

	dir := t.TempDir()

	missing := filepath.Join(dir, "absent")
	TokenFile = missing
	if _, err := TokenFromHome(); err == nil {
		t.Fatal("TokenFromHome() error = nil, want error for missing file")
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("  \n"), 0600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	TokenFile = empty
	if _, err := TokenFromHome(); err == nil {
		t.Fatal("TokenFromHome() error = nil, want error for empty file")
	}

	ok := filepath.Join(dir, "token")
	if err := os.WriteFile(ok, []byte("glpat-value\n"), 0600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	TokenFile = ok
	token, err := TokenFromHome()
	if err != nil {
		t.Fatalf("TokenFromHome() error = %v", err)
	}
	if token != "glpat-value" {
		t.Fatalf("TokenFromHome() = %q, want the trimmed token", token)
	}
}
