package adoption

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAdoptionMergeRequest(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("missing private token")
		}
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch {
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/repository/files/"):
			response.WriteHeader(http.StatusNotFound)
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/merge_requests"):
			_ = json.NewEncoder(response).Encode([]any{})
		case strings.HasSuffix(request.URL.Path, "/repository/branches"):
			response.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(request.URL.Path, "/repository/commits"):
			response.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(request.URL.Path, "/merge_requests"):
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(map[string]string{"web_url": "https://gitlab.test/mr/1"})
		default:
			response.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewGitLabClient(server.URL, "test-token")
	client.client = server.Client()
	audit := Audit{Target: "192.0.2.10", Hostname: "node1", Architecture: "x86_64", Interfaces: []Interface{{Name: "eth0", MAC: "52:54:00:11:22:33"}}}
	url, err := client.OpenAdoptionMergeRequest(audit, []byte("hostname: node1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://gitlab.test/mr/1" || len(requests) != 5 {
		t.Fatalf("unexpected result url=%q requests=%v", url, requests)
	}
}

func TestOpenAdoptionMergeRequestRejectsOpenDraft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "/repository/files/") {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(response).Encode([]map[string]string{{
			"source_branch": "adopt/node1-20260826-130000",
			"web_url":       "https://gitlab.test/mr/4",
		}})
	}))
	defer server.Close()
	client := NewGitLabClient(server.URL, "test-token")
	client.client = server.Client()
	_, err := client.OpenAdoptionMergeRequest(Audit{Hostname: "node1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "https://gitlab.test/mr/4") {
		t.Fatalf("expected existing draft URL, got %v", err)
	}
}

func TestOpenAdoptionMergeRequestRejectsDuplicate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := NewGitLabClient(server.URL, "test-token")
	client.client = server.Client()
	_, err := client.OpenAdoptionMergeRequest(Audit{Hostname: "node1"}, nil)
	if err == nil || !strings.Contains(err.Error(), ErrAlreadyAdopted.Error()) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestValidateCredentialDistinguishesExpiredToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v4/user" {
			http.NotFound(response, request)
			return
		}
		if request.Header.Get("PRIVATE-TOKEN") == "valid" {
			response.WriteHeader(http.StatusOK)
			return
		}
		response.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	valid, err := NewGitLabClient(server.URL, "valid").ValidateCredential()
	if err != nil || !valid {
		t.Fatalf("valid credential rejected: valid=%t err=%v", valid, err)
	}
	valid, err = NewGitLabClient(server.URL, "expired").ValidateCredential()
	if err != nil || valid {
		t.Fatalf("expired credential accepted: valid=%t err=%v", valid, err)
	}
}
