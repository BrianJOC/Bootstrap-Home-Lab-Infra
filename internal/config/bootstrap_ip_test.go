package config_test

import (
	"net"
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func TestDeriveBootstrapServerIP(t *testing.T) {
	tests := []struct {
		name       string
		targetHost string
		wantIP     string // exact match; empty = any non-empty result accepted
		wantSkip   bool
		wantEmpty  bool // true = result IP must be empty
	}{
		{"bare IPv4",         "192.168.1.100",       "192.168.1.100", true, false},
		{"IPv4 with port",    "192.168.1.100:22",    "192.168.1.100", true, false},
		{"IPv4 non-std port", "192.168.1.100:2222",  "192.168.1.100", true, false},
		// localhost resolves to either 127.0.0.1 or ::1 depending on OS /etc/hosts;
		// we accept any loopback address, skip must be false.
		{"resolvable hostname", "localhost", "", false, false},
		{"unresolvable host",   "doesnotexist.invalid", "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip, skip := config.DeriveBootstrapServerIP(tc.targetHost)
			if skip != tc.wantSkip {
				t.Errorf("skip=%v want %v", skip, tc.wantSkip)
			}
			if tc.wantEmpty && ip != "" {
				t.Errorf("ip=%q want empty", ip)
			}
			if !tc.wantEmpty && tc.wantIP != "" && ip != tc.wantIP {
				t.Errorf("ip=%q want %q", ip, tc.wantIP)
			}
			// For "resolvable hostname": verify result is a valid loopback IP.
			if tc.name == "resolvable hostname" && !tc.wantEmpty {
				parsed := net.ParseIP(ip)
				if parsed == nil || !parsed.IsLoopback() {
					t.Errorf("expected loopback IP for localhost, got %q", ip)
				}
			}
		})
	}
}
