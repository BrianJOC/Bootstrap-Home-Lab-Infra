"""Repeatable experimental-release checks, confined to the disposable QEMU fixture.

status is read-only on the guest. standup and verify mutate only the test guest.
The JSON verdict fails closed on absent, stale, failed or missing coverage.
"""
import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import tarfile
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "logs/release-check"
spec = importlib.util.spec_from_file_location("dev_authorized", ROOT / "scripts/dev-authorized.py")
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)

# These are requirements, not manually editable pass flags. Implement an actual
# executable check before moving an entry into the measured gates below.
MISSING = {
    "raspberry-pi": "No physical Pi OS/kernel, storage, memory and full CI workload verification yet; QEMU ARM64 is not Pi hardware.",
}
GATES = {
    "doctor": "Local source prerequisites",
    "source": "Go/Ansible/Terraform source checks",
    "fixture-tests": "Release runner and acceptance regression tests",
    "generated": "Generated project, charts and real Terraform state lifecycle",
    "gateway": "Real access proxy with synthetic OIDC groups",
    "guest": "ARM64 Ubuntu guest, no-backup profile, k3s and GitOps runtime",
    "bootstrap": "Completed bootstrap command on this fixture",
    "fresh-install": "The complete bootstrap ran on a newly created guest from the base image",
    "merge-lifecycle": "GitLab MR deploy, core change, no-change rerun and deletion",
    "authentik-login": "Installed Authentik permits an allowed user and denies another",
    "full-resume": "Interrupted protected-main reconcile resumes with Terraform state and private values preserved",
    "seed-source-binding": "GitLab seed matches source files synchronized for this bootstrap",
}
HARDWARE_GATE = "raspberry-pi"
EVIDENCE_PREFIX = "RELEASE_EVIDENCE:"


def source_paths():
    result = subprocess.run(["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
                            cwd=ROOT, check=True, capture_output=True)
    roots = {"ansible", "bin", "charts", "cmd", "docs", "gitops", "internal", "scripts", "terraform"}
    names = {"go.mod", "go.sum", "Justfile", "requirements-dev.in", "requirements-dev.lock", ".gitlab-ci.yml"}
    files = []
    for name in sorted(set(result.stdout.decode().split("\0")) - {""}):
        path = Path(name)
        workflow = path.parts[:2] == (".github", "workflows") and path.suffix in {".yml", ".yaml"}
        if path.parts[0] not in roots and name not in names and not workflow:
            continue
        if any(p in {"collections", ".terraform", "__pycache__"} for p in path.parts):
            continue
        pin = len(path.parts) == 2 and path.parts[0] == "bin" and path.name.startswith(".") and path.name.endswith(".pkg")
        if any(p.startswith(".") for p in path.parts) and not (
                pin or workflow or name == ".gitlab-ci.yml" or path.name == ".terraform.lock.hcl"):
            continue
        if path.suffix in {".tfstate", ".tfplan", ".tfvars", ".pem", ".key", ".pyc"} or ".tfstate" in name or ".tfvars.json" in name:
            continue
        if name == "bin/bootstrap" or not (ROOT / path).exists():
            continue
        if (ROOT / path).is_symlink():
            target = os.readlink(ROOT / path)
            if path.parts[0] != "bin" or Path(target).name != target or target in {".", ".."}:
                raise RuntimeError("source contains an unsafe symlink")
        elif not (ROOT / path).is_file():
            continue
        files.append(name)
    return files


def fingerprint(files):
    digest = hashlib.sha256()
    for name in files:
        path = ROOT / name
        data = os.readlink(path).encode() if path.is_symlink() else path.read_bytes()
        kind = b"link" if path.is_symlink() else str(path.stat().st_mode & 0o777).encode()
        digest.update(name.encode() + b"\0" + kind + b"\0" + hashlib.sha256(data).digest())
    return digest.hexdigest()


def now():
    return datetime.now(timezone.utc).isoformat()


def failure_hint(log):
    """Extract fixed error categories, never echo provider output or secrets."""
    with log.open("rb") as handle:
        handle.seek(max(0, log.stat().st_size - 131072))
        tail = handle.read().decode(errors="replace")
    labels = []
    for text, label in (
        ("read operation timed out", "artifact download timed out"),
        ("Host key verification failed", "test SSH identity not trusted"),
        ("REMOTE HOST IDENTIFICATION HAS CHANGED", "test SSH identity changed"),
        ("Connection refused", "test guest or service is not listening"),
        ("HTTP 401", "synthetic API credential rejected/expired"),
        ("HTTP 403", "test API authorization denied"),
        ("No space left on device", "test disk is full"),
        ("Cannot connect to the Docker daemon", "Docker daemon unavailable"),
    ):
        if text.lower() in tail.lower():
            labels.append(label)
    for line in tail.splitlines():
        try:
            item = json.loads(line)
        except ValueError:
            continue
        if isinstance(item, dict) and item.get("msg") == "stage failed" and re.fullmatch(r"[a-z0-9-]{1,40}", str(item.get("stage", ""))):
            labels.append("bootstrap stage: " + item["stage"])
    return "; ".join(dict.fromkeys(labels))


def write_json(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def load_state(path, digest):
    if not path.exists():
        return {"schema": 1, "source": digest, "gates": {}}
    state = json.loads(path.read_text())
    if state.get("schema") != 1:
        raise RuntimeError("unsupported release-check state schema")
    if state.get("source") != digest:
        return {"schema": 1, "source": digest, "gates": {}, "previous_source": state.get("source")}
    return state


def verdict(state):
    checks = {name: state["gates"].get(name, {"status": "NOT_RUN", "detail": label})
              for name, label in GATES.items()}
    checks.update({name: {"status": "MISSING_TEST", "detail": reason}
                   for name, reason in MISSING.items() if name != HARDWARE_GATE})
    checks[HARDWARE_GATE] = {"status": "MISSING_TEST", "detail": MISSING[HARDWARE_GATE]}
    return {"schema": 1, "checked_at": now(), "source": state["source"],
            "ready": all(checks[name]["status"] == "PASS" for name in GATES),
            "physical_ready": all(checks[name]["status"] == "PASS" for name in (*GATES, HARDWARE_GATE)),
            "scope": "experimental-no-backup-qemu-gitlab-managed-web-service",
            "hardware_scope": "Raspberry Pi remains unverified until the physical gate passes",
            "checks": checks}


@contextmanager
def locked():
    OUT.mkdir(parents=True, mode=0o700, exist_ok=True)
    with (OUT / "lock").open("w") as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError("another release-check command is running") from None
        yield


def execute(argv, log, timeout, stdin=None):
    """Bound each local process group; remote operations also use guest timeout."""
    env = os.environ.copy()
    env["PATH"] = f"{ROOT / '.venv/bin'}:{ROOT / 'bin'}:{env.get('PATH', '')}"
    with log.open("wb") as output:
        log.chmod(0o600)
        with subprocess.Popen(argv, cwd=ROOT, env=env, stdin=stdin, stdout=output,
                              stderr=subprocess.STDOUT, start_new_session=True) as proc:
            try:
                return proc.wait(timeout=timeout)
            except (subprocess.TimeoutExpired, KeyboardInterrupt):
                os.killpg(proc.pid, signal.SIGTERM)
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait()
                return 124


class Runner:
    def __init__(self, state):
        self.state = state

    def record(self, name, status, detail, log=None):
        self.state["gates"][name] = {"status": status, "detail": detail, "at": now()}
        if log:
            self.state["gates"][name]["log"] = str(log.relative_to(ROOT))
        write_json(OUT / "state.json", self.state)
        print(f"{status:12} {name}: {detail}", flush=True)

    def run(self, name, argv, timeout, hint, stdin=None):
        log = OUT / f"{time.time_ns()}-{name}.log"
        self.record(name, "RUNNING", "command in progress", log)
        print(f"RUN          {name} (budget {timeout}s); log: {log.relative_to(ROOT)}", flush=True)
        code = execute(argv, log, timeout, stdin)
        failure = failure_hint(log) if code else ""
        self.record(name, "PASS" if code == 0 else "FAIL",
                    "command completed" if code == 0 else f"exit {code}; {failure + '; ' if failure else ''}{hint}", log)
        return code == 0

    def task(self, name, recipe, timeout=1800):
        return self.run(name, [str(ROOT / "bin/just"), recipe], timeout,
                        f"inspect log; retry bin/just {recipe}")

    def guest(self, name, command, timeout=120):
        # GNU timeout runs inside Linux too: losing SSH must not leave an
        # unlimited remote bootstrap running while another invocation starts.
        remote = "timeout --signal=TERM --kill-after=15s " + str(max(1, timeout - 20))
        remote += "s sh -c " + shlex.quote(command)
        return self.run(name, dev.ssh_command(remote), timeout,
                        "inspect guest log; fix the first failing stage, then retry")


PROBE = r'''
import json,pathlib,platform,subprocess
root=pathlib.Path('/home/ansible/bootstrap-gitops-test')
def ok(args):
    try:
        return subprocess.run(args,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15).returncode==0
    except (OSError,subprocess.TimeoutExpired):
        return False
checks={
 'arm64':platform.machine()=='aarch64',
 'ubuntu': 'ID=ubuntu' in pathlib.Path('/etc/os-release').read_text(),
 'k3s_ready':ok(['sudo','-n','/usr/local/bin/k3s','kubectl','get','--raw=/readyz']),
 'gitops_runtime':ok(['sudo','-n','grep','-Fxq','gitops-runtime=1','/var/lib/bootstrap-home-infra/gitops-runtime-configured']),
 'gitops_executor':ok(['sudo','-n','test','-x','/usr/local/sbin/bootstrap-gitops-host']),
}
try:
 import yaml
 cfg=yaml.safe_load((root/'test/gitops-config.yaml').read_text())
 checks['no_backup_profile']=cfg.get('backup_enabled') is False and cfg.get('controller_recovery_enabled') is False and cfg.get('k3s_enabled') is True
except Exception:
 checks['no_backup_profile']=False
identity=pathlib.Path('/etc/machine-id').read_text().strip()+':'+pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()
print(json.dumps({'checks':checks,'failed':[k for k,v in checks.items() if not v],'fixture':identity},sort_keys=True))
raise SystemExit(0 if all(checks.values()) else 1)
'''


def probe(runner, completed_bootstrap=False):
    command = f"cd {dev.GUEST_ROOT} && .venv/bin/python -c " + shlex.quote(PROBE)
    passed = runner.guest("guest", command, 120)
    log = ROOT / runner.state["gates"]["guest"]["log"]
    for line in log.read_text(errors="replace").splitlines():
        try:
            result = json.loads(line)
        except ValueError:
            continue
        if isinstance(result, dict) and isinstance(result.get("failed"), list):
            identity = result.get("fixture")
            if identity != runner.state.get("fixture"):
                runner.state["gates"].pop("merge-lifecycle", None)
                runner.state["gates"].pop("authentik-login", None)
                runner.state["gates"].pop("full-resume", None)
                runner.state["gates"].pop("seed-source-binding", None)
                runner.state.pop("acceptance_checkpoint", None)
                runner.state["fresh_install_candidate"] = False
                if not completed_bootstrap:
                    runner.state["gates"].pop("bootstrap", None)
                    runner.state["gates"].pop("fresh-install", None)
                runner.state["fixture"] = identity
                write_json(OUT / "state.json", runner.state)
            # Only names from our fixed probe; never echo arbitrary command logs.
            failed = [s for s in result["failed"] if s in
                      {"arm64", "ubuntu", "k3s_ready", "gitops_runtime", "gitops_executor", "no_backup_profile"}]
            if failed:
                runner.record("guest", "FAIL", "missing/failed: " + ", ".join(failed), log)
    return passed


def sync_source(runner, files):
    # Archive declared sources only, never controller state, .env or user keys.
    # The sole private key is the existing, fixed disposable-VM test key.
    key = ROOT / "test/keys/test_id_ed25519"
    if not key.is_file() or key.is_symlink():
        runner.record("sync", "FAIL", "fixed disposable test key is missing or is a symlink")
        return False
    source_manifest = {"schema": 1, "source": fingerprint(files),
                       "files": {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest()
                                 for name in files}}
    manifest_bytes = (json.dumps(source_manifest, sort_keys=True, separators=(",", ":")) + "\n").encode()
    with tempfile.TemporaryFile() as archive:
        with tarfile.open(fileobj=archive, mode="w") as tar:
            for name in [*files, "test/keys/test_id_ed25519", "test/keys/test_id_ed25519.pub"]:
                tar.add(ROOT / name, arcname=name, recursive=False)
            item = tarfile.TarInfo("test/release-source.json")
            item.mode, item.size = 0o600, len(manifest_bytes)
            tar.addfile(item, io.BytesIO(manifest_bytes))
        archive.seek(0)
        extract = "import sys,tarfile; a=tarfile.open(fileobj=sys.stdin.buffer,mode='r|'); a.extractall(" + repr(dev.GUEST_ROOT) + ",filter='data')"
        command = "umask 077; python3 -c " + shlex.quote(extract)
        return runner.run("sync", dev.ssh_command("sh -c " + shlex.quote(command)), 180,
                          "source transfer failed; SSH identity must already be trusted", archive)


def guest_json(runner, name, code):
    """Run a fixed guest-side evidence reader and return its sanitized JSON record."""
    command = f"cd {dev.GUEST_ROOT} && .venv/bin/python -c " + shlex.quote(code)
    if not runner.guest(name, command, 120):
        return None
    log = ROOT / runner.state["gates"][name]["log"]
    for line in log.read_text(errors="replace").splitlines():
        if line.startswith(EVIDENCE_PREFIX):
            try:
                value = json.loads(line[len(EVIDENCE_PREFIX):])
            except json.JSONDecodeError:
                return None
            return value if isinstance(value, dict) else None
    return None


SEED_EVIDENCE_READER = r'''import json,pathlib,socket
root=pathlib.Path('/home/ansible/bootstrap-gitops-test')
evidence=json.loads((root/'logs/release-check/seed-source-binding.json').read_text())
manifest=json.loads((root/'test/release-source.json').read_text())
identity=pathlib.Path('/etc/machine-id').read_text().strip()+':'+pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()
print('RELEASE_EVIDENCE:'+json.dumps({'evidence':evidence,'manifest_source':manifest.get('source'),'fixture':identity},sort_keys=True))
'''


LIFECYCLE_EVIDENCE_READER = r'''import json,pathlib
root=pathlib.Path('/home/ansible/bootstrap-gitops-test')
checkpoint=json.loads(pathlib.Path(CHECKPOINT_PATH).read_text())
auth=json.loads((root/'logs/release-check/authentik-login.json').read_text())
resume=json.loads((root/'logs/release-check/full-resume.json').read_text())
identity=pathlib.Path('/etc/machine-id').read_text().strip()+':'+pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()
print('RELEASE_EVIDENCE:'+json.dumps({'checkpoint':checkpoint,'authentik':auth,'resume':resume,'fixture':identity},sort_keys=True))
'''


def verify_seed_evidence(value, source, fixture):
    if not isinstance(value, dict):
        return False, "seed source evidence is absent or unreadable"
    evidence = value.get("evidence")
    commit = evidence.get("main_sha") if isinstance(evidence, dict) else None
    passed = (
        isinstance(evidence, dict) and evidence.get("schema") == 1
        and evidence.get("status") == "PASS"
        and evidence.get("source_fingerprint") == source
        and value.get("manifest_source") == source
        and value.get("fixture") == fixture
        and isinstance(commit, str) and re.fullmatch(r"[0-9a-f]{40,64}", commit)
        and isinstance(evidence.get("matched_files"), int) and evidence["matched_files"] >= 25
    )
    return passed, f"seed files compared against synchronized source at {commit}" if passed else "seed source evidence is failed, stale, or bound to another source/fixture"


def verify_lifecycle_evidence(value, source, fixture):
    if not isinstance(value, dict):
        return {"authentik-login": (False, "installed Authentik evidence is absent or unreadable"),
                "full-resume": (False, "full-resume evidence is absent or unreadable")}
    checkpoint = value.get("checkpoint")
    auth, resume = value.get("authentik"), value.get("resume")
    outcomes = auth.get("outcomes") if isinstance(auth, dict) else None
    auth_outcomes_valid = isinstance(outcomes, dict)
    current_fixture = value.get("fixture") == fixture
    checkpoint_bound = (isinstance(checkpoint, dict)
                        and type(checkpoint.get("deploy_pipeline")) is int and checkpoint["deploy_pipeline"] > 0
                        and isinstance(checkpoint.get("deploy_pipeline_sha"), str)
                        and re.fullmatch(r"[0-9a-f]{7,64}", checkpoint["deploy_pipeline_sha"])
                        and isinstance(checkpoint.get("deploy_head_sha"), str)
                        and re.fullmatch(r"[0-9a-f]{7,64}", checkpoint["deploy_head_sha"])
                        and type(checkpoint.get("removal_pipeline")) is int and checkpoint["removal_pipeline"] > 0
                        and isinstance(checkpoint.get("removal_pipeline_sha"), str)
                        and re.fullmatch(r"[0-9a-f]{7,64}", checkpoint["removal_pipeline_sha"])
                        and checkpoint.get("deploy_verified") is True
                        and checkpoint.get("no_change_verified") is True
                        and checkpoint.get("removal_verified") is True
                        and checkpoint.get("removal_rerun_verified") is True)
    auth_ok = (current_fixture and checkpoint_bound and isinstance(auth, dict)
               and auth.get("schema") == 1 and auth.get("test") == "installed-authentik-allowed-denied-login"
               and auth.get("status") == "PASS" and auth.get("source_fingerprint") == source
               and auth.get("pipeline_id") == checkpoint.get("deploy_pipeline")
               and auth.get("pipeline_sha") == checkpoint.get("deploy_pipeline_sha")
               and auth.get("source_sha") == checkpoint.get("deploy_pipeline_sha")
               and auth.get("merge_head_sha") == checkpoint.get("deploy_head_sha")
               and checkpoint.get("authentik_login_verified") is True
               and checkpoint.get("authentik_login_source_fingerprint") == source
               and checkpoint.get("authentik_login_pipeline_id") == checkpoint.get("deploy_pipeline")
               and checkpoint.get("authentik_login_pipeline_sha") == checkpoint.get("deploy_pipeline_sha")
               and checkpoint.get("authentik_login_head_sha") == checkpoint.get("deploy_head_sha")
               and auth.get("identities_cleaned") is True
               and auth_outcomes_valid
               and outcomes.get("allowed_http_status") == 200
               and outcomes.get("allowed_reached_application") is True
               and outcomes.get("denied_http_status") == 403
               and outcomes.get("denied_forbidden") is True)
    resume_ok = (current_fixture and checkpoint_bound and isinstance(resume, dict)
                 and resume.get("schema") == 1 and resume.get("status") == "PASS"
                 and resume.get("source_fingerprint") == source
                 and resume.get("deploy_sha") == checkpoint.get("deploy_pipeline_sha")
                 and checkpoint.get("full_resume_verified") is True
                 and checkpoint.get("full_resume_source_fingerprint") == source
                 and checkpoint.get("full_resume_pipeline_sha") == checkpoint.get("deploy_pipeline_sha")
                 and resume.get("interrupted_pipeline_sha") == checkpoint.get("deploy_pipeline_sha")
                 and resume.get("retry_pipeline_sha") == checkpoint.get("deploy_pipeline_sha")
                 and resume.get("state_preserved") is True and resume.get("secrets_preserved") is True
                 and resume.get("cancel_observed") is True
                 and type(resume.get("interrupted_pipeline_id")) is int and resume["interrupted_pipeline_id"] > 0
                 and type(resume.get("retry_pipeline_id")) is int and resume["retry_pipeline_id"] > 0
                 and resume.get("retry_pipeline_id") != resume.get("interrupted_pipeline_id")
                 and resume.get("interrupted_pipeline_status") in {"canceled", "failed"}
                 and isinstance(resume.get("states_compared"), int) and resume["states_compared"] > 0)
    return {
        "authentik-login": (auth_ok, "installed Authentik allowed/denied evidence matches the active deploy pipeline" if auth_ok else "installed Authentik evidence is failed, stale, or bound to another source/fixture/deploy"),
        "full-resume": (resume_ok, "interruption and recovery evidence matches the active deploy commit" if resume_ok else "full-resume evidence is failed, stale, or bound to another source/fixture/deploy"),
    }


def standup(runner, files):
    if not runner.task("integration-prerequisites", "doctor-integration", 120):
        return False
    if not runner.task("mocks", "mocks-up", 900):
        return False
    # Never call vm-down here: it deletes the snapshot. Refuse to overwrite it.
    pid = Path(tempfile.gettempdir()) / "bootstrap-home-infra-test-vm.pid"
    if pid.exists():
        value = int(pid.read_text().strip())
        if value <= 1:
            raise RuntimeError("invalid disposable VM PID file")
        try:
            os.kill(value, 0)
        except ProcessLookupError:
            pid.unlink()  # Only a positively dead PID; never kill an unknown process.
    fresh_install_candidate = bool(runner.state.get("fresh_install_candidate"))
    if not pid.exists():
        recipe = "vm-resume" if (ROOT / "test/images/run-snapshot.qcow2").exists() else "vm-up"
        if not runner.task("vm", recipe, 180):
            return False
        if recipe == "vm-up":
            fresh_install_candidate = True
            runner.state["fresh_install_candidate"] = True
            write_json(OUT / "state.json", runner.state)
    if not runner.guest("identity", "true", 40) or not sync_source(runner, files):
        return False
    command = f"cd {dev.GUEST_ROOT} && bash scripts/test-gitops-guest-bootstrap.sh"
    if not runner.guest("bootstrap", command, 10820):
        return False
    if not probe(runner, completed_bootstrap=True):
        return False
    if fresh_install_candidate:
        runner.record("fresh-install", "PASS", "full bootstrap completed on the guest created from the ARM64 base image")
    return True


def verify(runner):
    # Capture the prior result before invalidating acceptance gates. A completed
    # run starts a new lifecycle; an unfinished checkpoint remains resumable.
    previous_lifecycle_pass = runner.state["gates"].get("merge-lifecycle", {}).get("status") == "PASS"
    new_lifecycle = not runner.state.get("acceptance_checkpoint") or previous_lifecycle_pass
    if new_lifecycle:
        runner.state["acceptance_checkpoint"] = f"logs/release-acceptance-{time.time_ns()}.json"
        write_json(OUT / "state.json", runner.state)
    for name in ("seed-source-binding", "merge-lifecycle", "authentik-login", "full-resume"):
        runner.record(name, "NOT_RUN", "verification attempt has not collected current evidence")

    if not probe(runner):
        return False
    if not sync_source(runner, source_paths()):
        return False

    # A changed fixture clears the checkpoint in probe(). Start with fresh guest
    # state if that happened; otherwise retain an interrupted lifecycle's path.
    checkpoint = runner.state.get("acceptance_checkpoint")
    if not checkpoint:
        new_lifecycle = True
        runner.state["acceptance_checkpoint"] = f"logs/release-acceptance-{time.time_ns()}.json"
        write_json(OUT / "state.json", runner.state)
        checkpoint = runner.state["acceptance_checkpoint"]
    if new_lifecycle:
        for name in ("authentik-login", "full-resume"):
            if runner.state["gates"].get(name, {}).get("status") != "NOT_RUN":
                runner.record(name, "NOT_RUN", "awaiting evidence from the new acceptance lifecycle")
    fixture = runner.state.get("fixture")
    if not isinstance(fixture, str) or not fixture:
        runner.record("seed-source-binding", "FAIL", "guest fixture identity is missing")
        return False

    # This comparison must run before the acceptance fixture edits main. It
    # proves the GitLab seed matches the source synchronized for this guest.
    runner.record("seed-source-binding", "RUNNING", "comparing seeded GitLab files with synchronized source")
    seed_command = f"cd {dev.GUEST_ROOT} && .venv/bin/python scripts/test-gitops-seed-source.py"
    seed_ran = runner.guest("seed-source-test", seed_command, 180)
    seed_value = guest_json(runner, "seed-source-evidence", SEED_EVIDENCE_READER) if seed_ran else None
    seed_ok, seed_detail = verify_seed_evidence(seed_value, runner.state["source"], fixture)
    runner.record("seed-source-binding", "PASS" if seed_ok else "FAIL",
                  f"{seed_detail}; checkpoint {Path(checkpoint).name}")
    if not seed_ok:
        return False

    command = (f"cd {dev.GUEST_ROOT} && .venv/bin/python scripts/test-gitops-guest-merge.py"
               " --source-fingerprint " + shlex.quote(runner.state["source"])
               + " --checkpoint " + shlex.quote(checkpoint))
    lifecycle_ok = runner.guest("merge-lifecycle", command, 14420)
    if not lifecycle_ok:
        for name in ("authentik-login", "full-resume"):
            runner.record(name, "FAIL", "acceptance lifecycle did not complete; no passing evidence collected")
        return False

    # Merge exit status is necessary but not sufficient. Read and bind each
    # generated record to the current fixture, source, and saved checkpoint.
    code = LIFECYCLE_EVIDENCE_READER.replace("CHECKPOINT_PATH", repr(str(Path(dev.GUEST_ROOT) / checkpoint)))
    lifecycle_value = guest_json(runner, "lifecycle-evidence", code)
    evidence_results = verify_lifecycle_evidence(lifecycle_value, runner.state["source"], fixture)
    for name, (passed, detail) in evidence_results.items():
        runner.record(name, "PASS" if passed else "FAIL", detail)
    return all(passed for passed, _ in evidence_results.values())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("status", "source", "standup", "verify"))
    args = parser.parse_args()
    os.umask(0o077)
    with locked():
        files = source_paths()
        state = load_state(OUT / "state.json", fingerprint(files))
        runner = Runner(state)
        pending = verdict(state)
        pending.update(ready=False, command=args.command, command_passed=False, running=True)
        write_json(OUT / "result.json", pending)
        action_ok = True
        if args.command == "source":
            for name, task, budget in (("doctor", "doctor", 120),
                                       ("fixture-tests", "test-release-check", 120),
                                       ("source", "lint-all", 3600),
                                       ("generated", "test-gitops-generated", 1800),
                                       ("gateway", "test-gitops-access", 360)):
                if not runner.task(name, task, budget):
                    action_ok = False
                    break
        elif args.command == "standup":
            action_ok = standup(runner, files)
        elif args.command == "verify":
            action_ok = verify(runner)
        else:
            action_ok = probe(runner)
        if fingerprint(source_paths()) != state["source"]:
            for check in state["gates"].values():
                if check["status"] == "PASS":
                    check.update(status="STALE", detail="source changed while this command was running; rerun")
            write_json(OUT / "state.json", state)
            action_ok = False
        report = verdict(state)
        report["command"] = args.command
        report["command_passed"] = action_ok
        write_json(OUT / "result.json", report)
        print("\nREADY: " + ("YES" if report["ready"] else "NO"))
        for name, check in report["checks"].items():
            print(f"{check['status']:12} {name}: {check['detail']}")
        print("Result: logs/release-check/result.json")
        # status answers overall readiness; actions answer whether that action
        # passed. JSON always exposes both so CI cannot mistake a partial pass.
        return 0 if (report["ready"] if args.command == "status" else action_ok) else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"ERROR: release runner could not complete ({type(error).__name__}); no readiness claim")
        raise SystemExit(2) from None
