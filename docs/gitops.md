# GitLab management handoff and web services

Status: source implemented; focused source and disposable-component evidence
exists. Full GitLab merge-to-service acceptance on a newly bootstrapped platform
remains pending.
This implements the existing [ownership contract](resource-ownership.md), not
a replacement architecture. Node expansion is outside this implementation.

## What takes over after bootstrap

Stage 6 seeds `infrastructure/iac` with the Go runner, Terraform roots, Ansible
roles, charts, tests, pinned Hermit tools and public platform configuration.
It retains the existing Terraform file-resource addresses. Seeded file content
is ignored on subsequent Terraform refreshes so Git owns reviewed edits.

The final `gitops` stage runs after MAAS, optional k3s and lifecycle setup. It
puts provider credentials and minimum runtime secrets into Vault and installs
a root-owned host helper. A protected local copy of the day-2 GitLab API token
allows an interrupted handoff to resume without minting another token. The
token expires after one year; an inactive stored token requires deliberate
renewal and is never silently rotated.

The generated project contains:

| Path | Purpose |
| --- | --- |
| `platform/platform.json` | Platform identity, existing state roots, cluster targets |
| `platform/bootstrap-*.tfvars.json` | Reviewed configuration of the existing core API objects |
| `platform/host.json` | Public host/runtime configuration |
| `services/*.yaml` | Declarative web services |
| `terraform/`, `ansible/`, `cmd/gitops/` | Their existing resource owners and orchestrator |
| `.gitlab-ci.yml` | Validate, merge-request plan and protected-main reconciliation |
| `bin/restore-links` | Restore Hermit symlinks materialized by GitLab's file API |

The pipeline authenticates to Vault with project-bound GitLab ID tokens.
`gitlab-plan` and `gitlab-apply` are distinct roles; apply additionally requires
the protected `main` branch. This is a private, single-administrator management
project. Provider API credentials and cluster certificates are privileged;
the plan job is not a security sandbox for untrusted contributors.

Every core root reuses its existing GitLab HTTP state. Missing bootstrap or
cluster-registration state stops execution before Terraform initialization.
Day-2 neither imports resources nor migrates laptop state. New application
resources use `services-<cluster>` state. Deleting the last service still
reconciles that state with an empty module set.

One resource-group lease covers core configuration, workloads, Ansible,
health checks and the existing checkpoint hook. Main creates and applies fresh
saved plans from its own checkout. Merge-request artifacts contain only
resource addresses and actions. Sensitive variables, plan files and temporary
kubeconfigs are private and removed on success or failure. Terraform state
necessarily contains sensitive provider/resource attributes, including the
OIDC client and gateway cookie secrets; keep the GitLab project/state private.

The privileged helper independently validates the signed apply token against
its root-owned Vault identity. Runtime reconciliation reuses the host roles
without Vault initialization, GitLab bootstrap credential creation, MAAS image
imports or controller kubeconfig fetching. Existing retention approval is
preserved unless reviewed configuration explicitly overrides it. Manual Vault
unseal after a restart remains the accepted operating model.

## Deploy the included web application

In the generated GitLab project, copy `gitops/examples/whoami.yaml` to
`services/whoami.yaml`, set the platform domain and installed cluster name,
and open a merge request. The example is:

```yaml
name: whoami
hostname: whoami.home.example.com
target:
  cluster: bootstrap
  helm_chart: charts/whoami
  chart_version: 1.0.0
  service: whoami
  port: 80
  node_port: 31080
authentik: true
allowed_groups: [adults]
health_check: http:/health
```

Merge a passing pipeline. The runner adds the DNS record and Authentik client
through their existing state owners, then installs the access boundary and
workload through Terraform's Helm provider. The host's existing wildcard
certificate terminates HTTPS; nginx sends traffic to the cluster's access
gateway. OAuth2 Proxy validates Authentik's groups claim. Namespace network
policies restrict incoming application traffic to that gateway.

The gateway uses the same declared groups as the Authentik application. Assign
household users to their appropriate Authentik groups; declaring a service does
not automatically grant membership. An allowed login reaches the application;
a valid login without any allowed group receives HTTP 403.

The pipeline checks the declared application health path through the protected
Kubernetes API and requires the public HTTPS endpoint to redirect to the
platform's Authentik login. A generic 401/403/502 is not a successful health
check. A target-local timer repeats both checks and writes
`/var/lib/bootstrap-home-infra/gitops-services/status.json`. It uses protected
certificate-only kubeconfigs; credential command plugins are refused.

To remove a service, delete its YAML in a reviewed merge. The same DNS,
Authentik, Helm and nginx owners reconcile the deletion. Review persistent
storage retention separately; deleting a declaration is not a data-erasure
or recovery procedure.

## Supported chart contract and limits

Local charts live under `charts/`. Remote charts require an HTTPS `repository`
and exact `chart_version`; `values` is a project-relative file. Each service
gets its own `service-<name>` namespace and unique gateway NodePort per cluster.

The renderer permits Deployments, StatefulSets, Jobs, ClusterIP Services,
ConfigMaps, Secrets and PersistentVolumeClaims. Workload pods must disable
service-account token mounting and privilege escalation. Charts cannot install
an independent ingress, NodePort, additive NetworkPolicy, privileged/host
workload, or gateway impersonation label. These checks run before planning and
again as Helm's post-render gate. Charts needing other Kubernetes resources
require an explicit adapter; they fail closed rather than silently bypassing
the declared access policy.

The PRD's native-protocol (`authentik: false`) and bare-metal service examples
are still accepted future work. They are not implemented by this web adapter.
Native application-level group mapping, automatic Kuma monitor creation and
MR plan comments remain outside the implemented path. Plans are available as
CI job output/artifacts. No full merge-to-ready timing claim is made yet.

For an already handed-off installation, deliver the new source and pipeline
through a reviewed GitLab change and provision the new Vault roles/credential
inventory and trusted helper before enabling reconciliation. Re-running the
old laptop's migrated Terraform roots is not an upgrade procedure. This
repository change has not mutated a real homelab or published remote changes.

## Validation and remaining acceptance

```bash
bin/just doctor
bin/just lint-all
bin/just test-gitops-generated
bin/just doctor-integration
bin/just test-gitops-access
# With the repository-owned ARM64 QEMU guest running:
bin/just test-gitops-handoff
```

The generated-project test runs its actual unit tests, restores and executes
its pinned tools, renders the example with Helm and validates the generated
Terraform service root. Real Terraform tests exercise fresh apply, no-change
rerun, interrupted apply and resume against disposable HTTP state. The
shipped OAuth2 Proxy image is tested with synthetic OIDC users in allowed and
denied groups. Unit tests cover missing state, branch gates, credential
cleanup, application health and access-boundary bypass attempts.

Recorded evidence is deliberately narrower than combined acceptance. On
September 27, source linting, generated-project validation, and access tests
passed. On September 28, the source and integration doctors passed, and the
actual disposable-guest administrator-preservation probe passed. October 4
inspection of the September 28 guest log confirms the Terraform-output fix
worked: GitLab seeding, seven component state handoffs and MAAS completed.
The run then failed downloading the checksum-verified k3s binary with a read
timeout. k3s and the final GitOps runtime marker remain absent; nothing is
currently running. Both release URLs respond from the guest today, but a full
download and the combined merge journey remain unproven. See the
[bounded completion plan](../reports/release-plan-2026-10-04.md).

Still required before declaring the complete product blocker closed: a full
GitLab MR → pipeline → DNS/TLS/Auth/workload/host journey on a fresh disposable
platform, a core configuration merge without the laptop, actual service
deletion/rerun and measured merge-to-ready time. Component tests are evidence
for their own boundaries, not a substitute for this combined acceptance run.

Protocol references: [OAuth2 Proxy configuration](https://oauth2-proxy.github.io/oauth2-proxy/configuration/overview/),
[Authentik OAuth2/OIDC provider](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/),
[GitLab Vault ID tokens](https://docs.gitlab.com/ci/secrets/hashicorp_vault/).
