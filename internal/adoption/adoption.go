package adoption

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AuditCommand performs a read-only inventory. Base64 protects free-form model
// names from the line-oriented transport without requiring extra target tools.
const AuditCommand = `set -Eeuo pipefail
encode() { printf '%s' "$1" | base64 | tr -d '\n'; }
printf 'hostname=%s\n' "$(hostname -s)"
printf 'fqdn_b64=%s\n' "$(encode "$(hostname -f)")"
printf 'architecture=%s\n' "$(uname -m)"
. /etc/os-release
printf 'operating_system_b64=%s\n' "$(encode "$ID $VERSION_ID")"
printf 'machine_id=%s\n' "$(sha256sum /etc/machine-id | awk '{print $1}')"
printf 'cpu_count=%s\n' "$(getconf _NPROCESSORS_ONLN)"
printf 'cpu_model_b64=%s\n' "$(encode "$(awk -F: '/model name|Model/{gsub(/^[ \t]+/, "", $2); print $2; exit}' /proc/cpuinfo)")"
printf 'memory_bytes=%s\n' "$(awk '/MemTotal/{printf "%.0f", $2 * 1024}' /proc/meminfo)"
while read -r name size type model; do
  [ "$type" = disk ] || continue
  printf 'disk=%s|%s|%s\n' "$name" "$size" "$(encode "$model")"
done < <(lsblk -bdn -o NAME,SIZE,TYPE,MODEL)
primary_interface="$(ip -o route show default 2>/dev/null | awk '{print $5; exit}')"
printf 'primary_interface=%s\n' "$primary_interface"
for interface_path in /sys/class/net/*; do
  name="${interface_path##*/}"
  [ "$name" = lo ] && continue
  printf 'interface=%s|%s\n' "$name" "$(cat "$interface_path/address")"
done
bmc_status=not-detected
bmc_address=
if [ -e /dev/ipmi0 ] || sudo -n dmidecode -t 38 >/dev/null 2>&1; then
  bmc_status=detected-no-credentials
  if command -v ipmitool >/dev/null 2>&1; then
    bmc_address="$(sudo -n ipmitool lan print 2>/dev/null | awk -F: '/IP Address[ \t]*:/{gsub(/[ \t]/, "", $2); print $2; exit}')"
    [ -z "$bmc_address" ] || bmc_status=detected-local-read
  fi
fi
printf 'bmc_status=%s\n' "$bmc_status"
printf 'bmc_address=%s\n' "$bmc_address"
`

type Disk struct {
	Name  string `yaml:"name" json:"name"`
	Bytes uint64 `yaml:"bytes" json:"bytes"`
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

type Interface struct {
	Name string `yaml:"name" json:"name"`
	MAC  string `yaml:"mac" json:"mac"`
}

type Audit struct {
	Target           string      `yaml:"target" json:"target"`
	Hostname         string      `yaml:"hostname" json:"hostname"`
	FQDN             string      `yaml:"fqdn" json:"fqdn"`
	Architecture     string      `yaml:"architecture" json:"architecture"`
	OperatingSystem  string      `yaml:"operating_system" json:"operating_system"`
	MachineID        string      `yaml:"machine_id" json:"machine_id"`
	CPUModel         string      `yaml:"cpu_model" json:"cpu_model"`
	CPUCount         int         `yaml:"cpu_count" json:"cpu_count"`
	MemoryBytes      uint64      `yaml:"memory_bytes" json:"memory_bytes"`
	Disks            []Disk      `yaml:"disks" json:"disks"`
	Interfaces       []Interface `yaml:"interfaces" json:"interfaces"`
	PrimaryInterface string      `yaml:"primary_interface,omitempty" json:"primary_interface,omitempty"`
	BMCStatus        string      `yaml:"bmc_status" json:"bmc_status"`
	BMCAddress       string      `yaml:"bmc_address,omitempty" json:"bmc_address,omitempty"`
}

type NodeDefinition struct {
	Hostname        string         `yaml:"hostname"`
	Target          string         `yaml:"target"`
	Architecture    string         `yaml:"architecture"`
	PXEMACAddress   string         `yaml:"pxe_mac_address"`
	PowerType       string         `yaml:"power_type"`
	PowerParameters map[string]any `yaml:"power_parameters"`
	Deploy          bool           `yaml:"deploy"`
	DistroSeries    string         `yaml:"distro_series"`
	Cluster         string         `yaml:"cluster,omitempty"`
	Audit           Audit          `yaml:"audit"`
}

func Parse(target, output string) (Audit, error) {
	audit := Audit{Target: target}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "hostname":
			audit.Hostname = value
		case "fqdn_b64":
			audit.FQDN = decode(value)
		case "architecture":
			audit.Architecture = value
		case "operating_system_b64":
			audit.OperatingSystem = decode(value)
		case "machine_id":
			audit.MachineID = value
		case "cpu_model_b64":
			audit.CPUModel = decode(value)
		case "cpu_count":
			audit.CPUCount, _ = strconv.Atoi(value)
		case "memory_bytes":
			audit.MemoryBytes, _ = strconv.ParseUint(value, 10, 64)
		case "disk":
			parts := strings.SplitN(value, "|", 3)
			if len(parts) == 3 {
				bytes, _ := strconv.ParseUint(parts[1], 10, 64)
				audit.Disks = append(audit.Disks, Disk{Name: parts[0], Bytes: bytes, Model: decode(parts[2])})
			}
		case "interface":
			parts := strings.SplitN(value, "|", 2)
			if len(parts) == 2 {
				audit.Interfaces = append(audit.Interfaces, Interface{Name: parts[0], MAC: parts[1]})
			}
		case "primary_interface":
			audit.PrimaryInterface = value
		case "bmc_status":
			audit.BMCStatus = value
		case "bmc_address":
			audit.BMCAddress = value
		}
	}
	if audit.Hostname == "" || audit.Architecture == "" || len(audit.Interfaces) == 0 {
		return Audit{}, fmt.Errorf("target audit is incomplete: hostname, architecture, and a network interface are required")
	}
	return audit, nil
}

func RenderNode(audit Audit) ([]byte, error) {
	architecture := map[string]string{"x86_64": "amd64/generic", "aarch64": "arm64/generic", "arm64": "arm64/generic"}[audit.Architecture]
	if architecture == "" {
		return nil, fmt.Errorf("unsupported MAAS architecture %q", audit.Architecture)
	}
	pxeMAC := audit.Interfaces[0].MAC
	for _, networkInterface := range audit.Interfaces {
		if networkInterface.Name == audit.PrimaryInterface {
			pxeMAC = networkInterface.MAC
			break
		}
	}
	definition := NodeDefinition{
		Hostname:        audit.Hostname,
		Target:          audit.Target,
		Architecture:    architecture,
		PXEMACAddress:   pxeMAC,
		PowerType:       "manual",
		PowerParameters: map[string]any{},
		Deploy:          true,
		DistroSeries:    "noble",
		Audit:           audit,
	}
	return yaml.Marshal(definition)
}

func MergeRequestDescription(audit Audit) string {
	bmc := audit.BMCStatus
	if audit.BMCAddress != "" {
		bmc += " (" + audit.BMCAddress + ")"
	}
	return fmt.Sprintf(`## Reviewed server adoption

| Item | Observed value |
|---|---|
| Target | %s |
| Hostname | %s |
| Architecture | %s |
| CPU | %s (%d cores) |
| Memory | %d bytes |
| Disks | %d |
| Interfaces | %d |
| BMC | %s |

The generated machine definition defaults to manual power control and Ubuntu
24.04 (noble) deployment. Add reviewed BMC credentials through Vault before
changing the power type. **Merging this MR authorizes MAAS to commission the
machine and replace its current operating system.** Cluster join remains empty
until the newly deployed SSH fingerprint has been deliberately confirmed.
`, audit.Target, audit.Hostname, audit.Architecture, audit.CPUModel, audit.CPUCount, audit.MemoryBytes, len(audit.Disks), len(audit.Interfaces), bmc)
}

func decode(value string) string {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(decoded))
}
