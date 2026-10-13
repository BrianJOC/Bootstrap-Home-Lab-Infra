package gitops

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProtectedKubeconfigKeepsTrustAndRejectsCommands(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	identity := server.TLS.Certificates[0]
	cert := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: identity.Certificate[0]}))
	key, err := x509.MarshalPKCS8PrivateKey(identity.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	private := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	config := fmt.Sprintf("clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n    certificate-authority-data: %s\nusers:\n- user:\n    client-certificate-data: %s\n    client-key-data: %s\n", cert, cert, private)
	protected, err := ProtectedKubeconfig([]byte(config), "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	client, address, err := ApplicationClient(protected)
	if err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()
	if address != "https://192.0.2.10:6443" || !strings.Contains(string(protected), cert) {
		t.Fatal("lost reviewed endpoint or CA identity")
	}
	for name, value := range map[string]string{
		"exec":     config + "    exec: {command: /bin/sh}\n",
		"insecure": strings.Replace(config, "    server:", "    insecure-skip-tls-verify: true\n    server:", 1),
		"file":     config + "    client-key: /root/private-key\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ProtectedKubeconfig([]byte(value), "192.0.2.10"); err == nil {
				t.Fatal("unsafe credential accepted")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthenticationHealthRejectsGenericErrors(t *testing.T) {
	for _, tc := range []struct {
		status   int
		location string
		healthy  bool
	}{
		{302, "https://authentik.home.example.com/application/o/authorize/?client_id=whoami", true},
		{200, "", false}, {401, "", false}, {403, "", false}, {502, "", false},
		{302, "https://untrusted.example/application/o/authorize/", false},
		{302, "http://authentik.home.example.com/application/o/authorize/", false},
	} {
		r := &Runner{platform: Platform{Domain: "home.example.com"}, services: []Service{{Name: "whoami", Hostname: "whoami.home.example.com"}}, Output: io.Discard,
			HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{tc.location}}, Body: http.NoBody, Request: req}, nil
			})}}
		if err := r.checkServices(context.Background()); (err == nil) != tc.healthy {
			t.Errorf("status %d redirect %q: %v", tc.status, tc.location, err)
		}
	}
}

func TestDeclaredApplicationHealthPath(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/service-whoami/services/http:whoami:80/proxy/health" {
			t.Errorf("wrong health path: %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	s := Service{Name: "whoami", HealthCheck: "http:/health", Target: Target{Namespace: "service-whoami", Service: "whoami", Port: 80}}
	if err := checkApplication(context.Background(), server.Client(), server.URL, s); err != nil {
		t.Fatal(err)
	}
	status = 503
	if err := checkApplication(context.Background(), server.Client(), server.URL, s); err == nil {
		t.Fatal("unhealthy application accepted")
	}
}
