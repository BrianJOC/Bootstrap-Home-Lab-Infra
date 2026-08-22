# UniFi Guide: Extend Technitium DNS Beyond the Homelab

> This is an optional, post-bootstrap guide. UI labels can move between UniFi
> Network releases; consult the linked Ubiquiti help pages if a label differs.

## Scope

Use this guide after Technitium is healthy for the homelab. It makes Technitium
the DNS resolver advertised to clients on additional UniFi networks. It does not
move DHCP for those networks to MAAS. UniFi remains their DHCP authority.

The dedicated MAAS provisioning VLAN is the exception: if explicitly enabled,
MAAS owns DHCP there and UniFi DHCP must be disabled for that VLAN.

## Before changing UniFi

Record:

- the Technitium server's stable IP;
- every network to be migrated;
- the current DNS setting for each network;
- one client available for lease renewal and testing on each network; and
- a maintenance window in which temporary DNS loss is acceptable.

Confirm from a client on the first network that direct queries to the Technitium
IP resolve both an internal service and an external name.

## Roll out one network at a time

Start with the homelab or another low-impact network. In UniFi Network:

1. Open **Settings → Networks** and select the network.
2. Find **DHCP Service Management → DNS Server**.
3. Disable the automatic DNS-server setting and enter the Technitium IP.
4. Apply the change.
5. Renew the test client's DHCP lease or reconnect it to the network.
6. Confirm the client received the Technitium IP as its DNS server.
7. Resolve an internal service name and an ordinary internet name.
8. Open an internal HTTPS service and confirm the expected private address and
   trusted certificate.
9. Observe the network before repeating the process for the next network.

Ubiquiti documents the per-network DNS field in its
[DNS troubleshooting guide](https://help.ui.com/hc/en-us/articles/17484948645015-UniFi-DNS-Troubleshooting-Guide)
and [local DNS guide](https://help.ui.com/hc/en-us/articles/15179064940439-UniFi-DNS-Records-and-Local-Hostnames).

Do not enter a public resolver beside a single Technitium IP as a client-side
fallback. Some clients will query either address, which makes private lookups
intermittent. Advertise two addresses only when both are internal resolvers that
serve the same private namespace.

## Content filtering and DNS forwarding

UniFi content filtering can redirect DNS and may prevent clients from using the
resolver advertised by DHCP. Review filtering for the migrated network if the
client does not query Technitium. Ubiquiti describes the interaction in
[Content and Domain Filtering](https://help.ui.com/hc/en-us/articles/12568927589143-Content-and-Domain-Filtering-in-UniFi).

UniFi also supports forwarding a domain to a dedicated internal DNS server. That
is a useful rollback or limited-scope alternative when you want every network to
resolve the homelab namespace but do not yet want every DNS query to traverse
Technitium. It is not the selected final state for this repository; the selected
final state advertises Technitium directly per network.

## Dedicated MAAS VLAN

Before approving MAAS DHCP:

1. Verify the VLAN ID, subnet, gateway, and switch-port tagging.
2. Verify the bootstrap server interface receives that VLAN.
3. In **Settings → Networks**, select only the dedicated lab network and disable
   its UniFi DHCP service.
4. Confirm the action in the bootstrap TUI so it can repeat the conflict probe
   and enable MAAS DHCP.
5. PXE boot one disposable client and verify its lease, gateway, DNS server, and
   MAAS discovery.

Do not disable UniFi DHCP on ordinary home networks. Ubiquiti's
[DHCP server guide](https://help.ui.com/hc/en-us/articles/360012097513-UniFi-DHCP-Server)
describes per-network DHCP configuration. If UniFi switches are in use, DHCP
Guarding can restrict accepted server addresses; update its trusted server list
for the MAAS VLAN as described in Ubiquiti's
[rogue DHCP guide](https://help.ui.com/hc/en-us/articles/19154105498007-Duplicate-IP-Addresses-and-Rogue-DHCP-Servers).

For tagged networks, verify every link in the path permits the VLAN. Ubiquiti's
[VLAN troubleshooting guide](https://help.ui.com/hc/en-us/articles/9592924981911-Virtual-Network-VLAN-Troubleshooting)
covers common tagging and trunk issues.

## Rollback

For a DNS rollout failure:

1. Return the affected UniFi network's DNS Server setting to its recorded prior
   value or automatic gateway setting.
2. Apply the change.
3. Renew the client lease.
4. Confirm external DNS is restored.
5. Leave other networks unchanged while diagnosing Technitium.

For a MAAS DHCP cutover failure:

1. Disable MAAS DHCP for the lab VLAN.
2. Re-enable the recorded UniFi DHCP configuration for only that VLAN.
3. Renew or reboot the test client.
4. Confirm it receives one lease from the expected server.

## Availability follow-up

Using one Technitium host for whole-home DNS makes that host a household
dependency. The initial rollout accepts this homelab tradeoff. Before treating
DNS as highly available, deploy and test a second internal resolver that serves
the same private zones, then advertise both internal addresses through UniFi.
