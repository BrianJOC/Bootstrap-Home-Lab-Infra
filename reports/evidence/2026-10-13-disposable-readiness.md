# Disposable readiness — 2026-10-13

Task 1B inspected the existing disposable fixture only. No source sync,
bootstrap, GitLab acceptance, snapshot replacement, or real infrastructure
operation was performed.

- Source doctor passed. Integration doctor first reported a stopped Docker
  daemon; Docker Desktop was opened, and the second integration doctor passed
  all prerequisites: Docker daemon, QEMU, Packer, base image, test key, and
  Hermit/source tools.
- The saved `test/images/run-snapshot.qcow2` existed (27G); the VM PID file was
  absent. The scoped `vm-resume` task completed and made SSH identity available.
- Fixed guest identity: `bootstrap-test`, user `ansible`, ARM64 Linux. The
  guest was newly booted at inspection time.
- Latest bootstrap log: `run-20261004T234214/bootstrap.log` (2026-10-04
  23:42 UTC). Sanitized stage markers show `config-collection` completed and
  `k3s` failed. No config values or log error payloads were collected.
- Current runtime: `k3s`, `containerd`, and Docker services active;
  `bootstrap-control-plane-1` Ready; six Kubernetes pods Running or Completed,
  with no other pod states reported.
- Guest process summary (process names/counts only): PostgreSQL 55,
  `svlogd` 13, `runsv` 13, Python 13, `docker-proxy` 10,
  `containerd-shim` 8, nginx 7, Ruby 6, `maas-netmon` 5,
  `tcpdump` 5, pause 4, sshd 3, Python 3, gunicorn 3, systemd 2.

There is no infrastructure prerequisite blocker now. The prior `k3s` stage
failure remains the saved bootstrap result even though the current cluster is
healthy at the node/pod summary level. After primary review of runner repairs,
the safe next command is the read-only scoped `task release-status`; do not
start lifecycle acceptance before that review.
