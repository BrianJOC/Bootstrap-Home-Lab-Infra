package adoption

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseAndRenderNode(t *testing.T) {
	encoded := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	output := strings.Join([]string{
		"hostname=node1",
		"fqdn_b64=" + encoded("node1.home.test"),
		"architecture=aarch64",
		"operating_system_b64=" + encoded("ubuntu 24.04"),
		"machine_id=abc123",
		"cpu_count=4",
		"cpu_model_b64=" + encoded("Neoverse-N1"),
		"memory_bytes=8589934592",
		"disk=vda|107374182400|" + encoded("QEMU Disk"),
		"primary_interface=enp1s0",
		"interface=docker0|02:42:00:00:00:01",
		"interface=enp1s0|52:54:00:11:22:33",
		"bmc_status=not-detected",
	}, "\n")
	audit, err := Parse("192.0.2.10", output)
	if err != nil {
		t.Fatal(err)
	}
	if audit.CPUModel != "Neoverse-N1" || len(audit.Disks) != 1 || len(audit.Interfaces) != 2 {
		t.Fatalf("unexpected audit: %#v", audit)
	}
	node, err := RenderNode(audit)
	if err != nil {
		t.Fatal(err)
	}
	text := string(node)
	for _, expected := range []string{
		"hostname: node1",
		"architecture: arm64/generic",
		`pxe_mac_address: "52:54:00:11:22:33"`,
		"power_type: manual",
		"deploy: true",
		"distro_series: noble",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("node YAML missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "cluster:") {
		t.Fatalf("unconfirmed cluster assignment must be omitted:\n%s", text)
	}
	description := MergeRequestDescription(audit)
	for _, expected := range []string{"replace its current operating system", "SSH fingerprint"} {
		if !strings.Contains(description, expected) {
			t.Fatalf("merge request description missing %q:\n%s", expected, description)
		}
	}
}

func TestParseRejectsIncompleteAudit(t *testing.T) {
	if _, err := Parse("192.0.2.10", "hostname=node1\narchitecture=x86_64\n"); err == nil {
		t.Fatal("expected incomplete audit to fail")
	}
}
