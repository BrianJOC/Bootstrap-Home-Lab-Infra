package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// This test runs actual Terraform against a disposable HTTP state backend. It
// proves state reuse and saved-plan semantics independently of provider mocks.
func TestTerraformHTTPStateLifecycle(t *testing.T) {
	if os.Getenv("GITOPS_TOOL_TEST") != "1" {
		t.Skip("run just test-gitops-generated")
	}
	var mu sync.Mutex
	var state []byte
	var writes, locks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, pass, _ := req.BasicAuth()
		if user != "gitlab-ci-token" || pass != "synthetic-job-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(req.URL.Path, "/lock") {
			if req.Method == "POST" {
				locks++
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		switch req.Method {
		case "GET":
			if len(state) == 0 {
				w.WriteHeader(404)
			} else {
				_, _ = w.Write(state)
			}
		case "POST":
			state, _ = io.ReadAll(req.Body)
			writes++
		default:
			t.Errorf("unexpected state mutation: %s", req.Method)
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	project := t.TempDir()
	terraform, err := filepath.Abs("../../bin/terraform")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(project, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(terraform, filepath.Join(project, "bin/terraform")); err != nil {
		t.Fatal(err)
	}
	root := Root{State: "services-fixture", Directory: "terraform"}
	if err = writeFile(project, "terraform/main.tf", []byte("variable \"revision\" { type = string }\nresource \"terraform_data\" \"service\" { input = var.revision }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	r := &Runner{Project: project, work: t.TempDir(), Executor: CommandExecutor{}, HTTP: server.Client(), Output: output, Env: map[string]string{"CI_API_V4_URL": server.URL + "/api/v4", "CI_PROJECT_ID": "1", "CI_JOB_TOKEN": "synthetic-job-token"}}
	// The host mutation guard is simulated; every Terraform invocation is real.
	r.Executor = &guardExecutor{delegate: CommandExecutor{}}
	for _, scenario := range []struct {
		name, revision string
		fail           bool
	}{{"fresh", "one", false}, {"no-change", "one", false}, {"interrupted", "two", true}, {"resumed", "two", false}} {
		t.Run(scenario.name, func(t *testing.T) {
			mu.Lock()
			beforeWrites, previous := writes, append([]byte{}, state...)
			mu.Unlock()
			output.Reset()
			r.Executor.(*guardExecutor).fail = scenario.fail
			err := r.terraform(context.Background(), "apply", root, map[string]any{"revision": scenario.revision})
			if (err != nil) != scenario.fail {
				t.Fatalf("apply: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if scenario.fail && (!bytes.Equal(state, previous) || writes != beforeWrites) {
				t.Fatal("interrupted plan changed remote state")
			}
			if scenario.name == "no-change" && output.Len() != 0 {
				t.Fatalf("no-change rerun has resource changes: %s", output)
			}
			if !scenario.fail {
				var saved struct {
					Resources []struct {
						Instances []struct {
							Attributes struct {
								Input struct {
									Value string `json:"value"`
								} `json:"input"`
							} `json:"attributes"`
						} `json:"instances"`
					} `json:"resources"`
				}
				if err := json.Unmarshal(state, &saved); err != nil {
					t.Fatal(err)
				}
				if len(saved.Resources) != 1 || saved.Resources[0].Instances[0].Attributes.Input.Value != scenario.revision {
					t.Fatal("wrong persisted service revision")
				}
			}
		})
	}
	if locks < 4 {
		t.Fatalf("state locking not used: %d locks", locks)
	}
}

type guardExecutor struct {
	delegate Executor
	fail     bool
}

func (e *guardExecutor) Run(ctx context.Context, dir string, args []string, env map[string]string, input io.Reader) ([]byte, error) {
	if filepath.Base(args[len(args)-1]) == "bootstrap-gitlab-freeze-guard" {
		if e.fail {
			return nil, fmt.Errorf("injected interruption before apply")
		}
		return nil, nil
	}
	return e.delegate.Run(ctx, dir, args, env, input)
}
