package stages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestUpsertGitLabProjectVariableCreatesMissingMaskedValue(t *testing.T) {
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method)
		if request.Header.Get("PRIVATE-TOKEN") != "token" {
			t.Error("missing GitLab token")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["value"] != "secret-value" || payload["masked"] != true {
			t.Errorf("unexpected payload: %#v", payload)
		}
		if request.Method == http.MethodPut {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	if err := upsertGitLabProjectVariable(server.URL, "42", "token", "MAAS_API_KEY", "secret-value", false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{http.MethodPut, http.MethodPost}) {
		t.Fatalf("unexpected methods: %v", methods)
	}
}

func TestFindGitLabResourceAndInitializeEmptyProject(t *testing.T) {
	initialized := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("PRIVATE-TOKEN") != "token" {
			t.Error("missing GitLab token")
		}
		switch {
		case request.Method == http.MethodGet && request.URL.EscapedPath() == "/projects/infrastructure%2Fiac":
			_ = json.NewEncoder(response).Encode(map[string]any{"id": 42, "empty_repo": true})
		case request.Method == http.MethodPost && request.URL.Path == "/projects/42/repository/commits":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["branch"] != "main" {
				t.Errorf("unexpected initialization payload: %#v", payload)
			}
			initialized = true
			response.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	metadata, exists, err := findGitLabResource(server.URL, "token", "projects", "infrastructure/iac", false)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || metadata.ID != 42 || !metadata.EmptyRepo {
		t.Fatalf("unexpected metadata: exists=%t value=%#v", exists, metadata)
	}
	if err := initializeEmptyGitLabProject(server.URL, "token", metadata.ID, false); err != nil {
		t.Fatal(err)
	}
	if !initialized {
		t.Fatal("expected empty project initialization")
	}
}

func TestFindGitLabResourceReturnsMissing(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, exists, err := findGitLabResource(server.URL, "token", "groups", "missing", false)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("missing resource reported as existing")
	}
}
