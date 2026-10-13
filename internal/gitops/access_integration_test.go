package gitops

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The shipped OAuth2 Proxy image talks to an ephemeral OIDC provider and
// upstream with synthetic identities. This exercises actual authorization,
// including a valid login whose group is forbidden, rather than grepping flags.
func TestGatewayGroupEnforcement(t *testing.T) {
	if os.Getenv("GITOPS_DOCKER_TEST") != "1" {
		t.Skip("run just test-gitops-access")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	group, nonce, issuer := "adults", "", ""
	mux := http.NewServeMux()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	identity := httptest.NewUnstartedServer(mux)
	identity.Listener = listener
	identity.Start()
	defer identity.Close()
	issuer = "http://host.docker.internal:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "userinfo_endpoint": issuer + "/userinfo", "jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		nonce = r.URL.Query().Get("nonce")
		mu.Unlock()
		callback, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
		query := callback.Query()
		query.Set("code", "synthetic-code")
		query.Set("state", r.URL.Query().Get("state"))
		callback.RawQuery = query.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	})
	claims := func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return map[string]any{"iss": issuer, "aud": "whoami", "sub": "synthetic-user", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "email": "fixture@example.com", "email_verified": true, "groups": []string{group}}
	}
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		payload, _ := json.Marshal(claims())
		body := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
		digest := sha256.Sum256([]byte(body))
		signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access-token", "token_type": "Bearer", "expires_in": 3600, "id_token": body + "." + base64.RawURLEncoding.EncodeToString(signature)})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(claims()) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "protected application reached")
	})
	name := fmt.Sprintf("bootstrap-gitops-access-%d", time.Now().UnixNano())
	args := []string{"run", "--detach", "--rm", "--name", name, "--add-host", "host.docker.internal:host-gateway", "-p", "127.0.0.1::4180", "quay.io/oauth2-proxy/oauth2-proxy:v7.15.4", "--provider=oidc", "--oidc-issuer-url=" + issuer, "--client-id=whoami", "--client-secret=synthetic-client-secret", "--cookie-secret=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "--cookie-secure=false", "--scope=openid email profile groups", "--oidc-groups-claim=groups", "--allowed-group=adults", "--email-domain=*", "--code-challenge-method=S256", "--reverse-proxy=true", "--skip-provider-button=true", "--http-address=0.0.0.0:4180", "--upstream=" + issuer + "/"}
	if output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start disposable gateway: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", "--time", "2", name).Run() })
	output, err := exec.CommandContext(ctx, "docker", "port", name, "4180/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + strings.TrimSpace(string(output))
	noRedirect := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		response, err := noRedirect.Get(address + "/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		output, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("gateway not ready: %s", output)
	}
	response, err := noRedirect.Get(address + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 {
		t.Fatalf("anonymous status = %d", response.StatusCode)
	}
	for _, tc := range []struct {
		group  string
		status int
	}{{"adults", 200}, {"kids", 403}} {
		t.Run(tc.group, func(t *testing.T) {
			mu.Lock()
			group = tc.group
			mu.Unlock()
			jar, _ := cookiejar.New(nil)
			// The browser visits the same synthetic issuer through localhost;
			// Docker resolves host.docker.internal itself for back-channel calls.
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				address = strings.Replace(address, "host.docker.internal:", "127.0.0.1:", 1)
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			defer transport.CloseIdleConnections()
			browser := &http.Client{Jar: jar, Transport: transport, Timeout: 15 * time.Second}
			response, err := browser.Get(address + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(response.Body, 16384))
			if response.StatusCode != tc.status {
				t.Fatalf("group %s got HTTP %d: %s", tc.group, response.StatusCode, body)
			}
			if tc.status == 200 && !strings.Contains(string(body), "protected application reached") {
				t.Fatal("allowed user did not reach application")
			}
			if tc.status == 403 && strings.Contains(string(body), "protected application reached") {
				t.Fatal("denied user reached application")
			}
		})
	}
}
