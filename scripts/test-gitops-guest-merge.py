#!/usr/bin/env python3
"""Prove the disposable GitLab merge-to-service lifecycle.

The ignored checkpoint has only disposable branch/MR identifiers. Re-run with
no arguments after a failure; --resume-mr recovers a deploy MR if it is lost.
"""
import argparse
import base64
import json
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

BASE = "https://gitlab.lab.test/api/v4"
PROJECT_PATH = "infrastructure%2Fiac"
CHECKPOINT = Path("logs/gitops-guest-merge-checkpoint.json")
MUTATION_SUMMARY = re.compile(r"(?m)^(?:bootstrap-[\w-]+|k3s-[\w-]+|services-[\w-]+|maas-machines): (?:create|update|delete|replace)(?:/\w+)?(?:\s|$)")


class APIError(RuntimeError):
    """An API error whose text deliberately excludes a response body."""
    def __init__(self, method, path, status):
        super().__init__(f"GitLab {method} {path} returned HTTP {status}")
        self.status = status


class GitLab:
    def __init__(self, token):
        self.token, self.project = token, None

    def api(self, method, path, body=None, raw=False):
        payload = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(BASE + path, data=payload, method=method,
                                         headers={"PRIVATE-TOKEN": self.token, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                return response.read().decode() if raw else (json.load(response) if response.status != 204 else None)
        except urllib.error.HTTPError as error:
            raise APIError(method, path, error.code) from None
        except urllib.error.URLError as error:
            raise RuntimeError(f"GitLab {method} {path} could not be reached") from error

    @property
    def root(self):
        if self.project is None:
            self.project = self.api("GET", "/projects/" + PROJECT_PATH)
        return f"/projects/{self.project['id']}"

    def file(self, path, ref):
        try:
            item = self.api("GET", self.root + "/repository/files/" + urllib.parse.quote(path, safe="") + "?ref=" + urllib.parse.quote(ref, safe=""))
        except APIError as error:
            if error.status == 404:
                return None
            raise
        return base64.b64decode(item["content"]).decode()

    def mr(self, iid):
        return self.api("GET", self.root + f"/merge_requests/{iid}")


def require_guest():
    if socket.gethostname() != "bootstrap-test" or Path.home() != Path("/home/ansible"):
        raise SystemExit("this fixture only runs as ansible inside the disposable QEMU guest")


def load(path):
    if not path.exists():
        return {"version": 1}
    try:
        state = json.loads(path.read_text())
    except json.JSONDecodeError as error:
        raise RuntimeError(f"checkpoint {path} is invalid; record its MR IDs before removing it") from error
    if state.get("version") != 1:
        raise RuntimeError(f"checkpoint {path} has an unsupported version")
    return state


def save(path, state):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def mark(state, path, phase):
    state[phase] = True
    save(path, state)


def bind_checkpoint(state, path, key, sha, phases):
    if state.get(key) == sha:
        return
    for phase in phases:
        state.pop(phase, None)
    state[key] = sha
    save(path, state)


def wait_pipeline(gitlab, pipeline_id):
    deadline, previous = time.monotonic() + 3600, None
    while time.monotonic() < deadline:
        pipeline = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}")
        status = pipeline["status"]
        if status != previous:
            print(f"pipeline {pipeline_id}: {status}", flush=True)
            previous = status
        if status == "success":
            return
        if status in ("failed", "canceled", "skipped", "manual"):
            jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}/jobs")
            print(json.dumps([{"id": j["id"], "name": j["name"], "status": j["status"]} for j in jobs]), flush=True)
            raise RuntimeError(f"pipeline {pipeline_id} ended {status}; rerun to start a new pipeline")
        time.sleep(5)
    raise RuntimeError("disposable pipeline exceeded the one-hour test budget")


def successful_mr_plan(gitlab, iid, sha):
    pipelines = gitlab.api("GET", gitlab.root + f"/merge_requests/{iid}/pipelines")
    for pipeline in pipelines:
        if pipeline.get("sha") != sha or pipeline.get("status") != "success":
            continue
        jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline['id']}/jobs")
        if any(job.get("name") == "plan" and job.get("status") == "success" for job in jobs):
            return pipeline
    return None


def require_reconcile_success(gitlab, pipeline_id):
    jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}/jobs")
    if not any(job.get("name") == "reconcile" and job.get("status") == "success" for job in jobs):
        raise RuntimeError(f"protected-main pipeline {pipeline_id} has no successful reconcile job")


def ensure_mr_plan(gitlab, iid):
    mr = gitlab.mr(iid)
    sha = mr["sha"]
    if successful_mr_plan(gitlab, iid, sha):
        print(f"MR !{iid} has a successful plan for {sha[:12]}", flush=True)
        return sha
    pipeline = gitlab.api("POST", gitlab.root + f"/merge_requests/{iid}/pipelines")
    wait_pipeline(gitlab, pipeline["id"])
    jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline['id']}/jobs")
    if not any(job.get("name") == "plan" and job.get("status") == "success" for job in jobs):
        raise RuntimeError(f"MR !{iid} pipeline {pipeline['id']} succeeded without a successful plan job")
    completed = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline['id']}")
    if completed.get("sha") != sha:
        raise RuntimeError(f"MR !{iid} plan pipeline ran for a stale source SHA")
    return sha


def merge_and_apply(gitlab, iid, expected_head_sha=None):
    mr = gitlab.mr(iid)
    if mr.get("target_branch") != "main":
        raise RuntimeError(f"MR !{iid} targets {mr.get('target_branch')!r}, expected main")
    if expected_head_sha and mr.get("sha") != expected_head_sha:
        raise RuntimeError(f"MR !{iid} changed after its successful plan; rerun the acceptance fixture")
    if mr["state"] != "merged":
        merged = gitlab.api("PUT", gitlab.root + f"/merge_requests/{iid}/merge", {"sha": mr["sha"]})
        sha = merged.get("merge_commit_sha") or merged.get("squash_commit_sha")
    else:
        sha = mr.get("merge_commit_sha") or mr.get("squash_commit_sha")
    if not sha:
        raise RuntimeError(f"MR !{iid} has no merge commit SHA")
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        pipelines = gitlab.api("GET", gitlab.root + "/pipelines?ref=main&sha=" + urllib.parse.quote(sha, safe=""))
        matches = [pipeline for pipeline in pipelines if pipeline.get("ref") == "main" and pipeline.get("sha") == sha]
        if matches:
            pipeline = matches[0]
            if pipeline["status"] in ("failed", "canceled"):
                print(f"retrying failed protected-main pipeline {pipeline['id']} once", flush=True)
                gitlab.api("POST", gitlab.root + f"/pipelines/{pipeline['id']}/retry")
            wait_pipeline(gitlab, pipeline["id"])
            require_reconcile_success(gitlab, pipeline["id"])
            print(f"protected-main reconciliation for MR !{iid} completed", flush=True)
            return pipeline["id"], sha
        time.sleep(2)
    raise RuntimeError("merged commit did not trigger a protected-main pipeline")


def ensure_branch(gitlab, branch):
    try:
        gitlab.api("GET", gitlab.root + "/repository/branches/" + urllib.parse.quote(branch, safe=""))
    except APIError as error:
        if error.status != 404:
            raise
        gitlab.api("POST", gitlab.root + "/repository/branches", {"branch": branch, "ref": "main"})


def existing_mr(gitlab, branch):
    mrs = gitlab.api("GET", gitlab.root + "/merge_requests?state=opened&source_branch=" + urllib.parse.quote(branch, safe=""))
    return mrs[0]["iid"] if mrs else None


def core_content(gitlab, ref):
    dns_text, host_text = gitlab.file("platform/bootstrap-dns.tfvars.json", ref), gitlab.file("platform/host.json", ref)
    if dns_text is None or host_text is None:
        raise RuntimeError("seeded project is missing required core configuration")
    dns, host = json.loads(dns_text), json.loads(host_text)
    old = next((record for record in dns["records"] if record["name"] == "gitops-proof"), {})
    dns["records"] = [record for record in dns["records"] if record["name"] != "gitops-proof"]
    dns["records"].append({"name": "gitops-proof", "type": "A", "value": "10.0.2.15", "ttl": 301 if old.get("ttl") == 300 else 300})
    host["certificate_renewal_randomized_delay"] = "37m"
    return json.dumps(dns, indent=2) + "\n", json.dumps(host, indent=2) + "\n"


def ensure_deploy_mr(gitlab, state, path):
    if state.get("deploy_mr"):
        return state["deploy_mr"]
    branch = state.setdefault("deploy_branch", "test/gitops-acceptance-" + uuid.uuid4().hex[:12])
    save(path, state)
    ensure_branch(gitlab, branch)
    if not state.get("deploy_committed"):
        dns, host = core_content(gitlab, branch)
        service = Path("gitops/examples/whoami.yaml").read_text().replace("home.example.com", "lab.test")
        actions = [
            {"action": "update", "file_path": "platform/bootstrap-dns.tfvars.json", "content": dns},
            {"action": "update", "file_path": "platform/host.json", "content": host},
            {"action": "update" if gitlab.file("services/whoami.yaml", branch) else "create", "file_path": "services/whoami.yaml", "content": service},
        ]
        gitlab.api("POST", gitlab.root + "/repository/commits", {"branch": branch, "commit_message": "test: reconcile core configuration and a private web service", "actions": actions})
        mark(state, path, "deploy_committed")
    recovered = existing_mr(gitlab, branch)
    if recovered:
        state["deploy_mr"] = recovered
        save(path, state)
        return recovered
    mr = gitlab.api("POST", gitlab.root + "/merge_requests", {"source_branch": branch, "target_branch": "main", "title": "Disposable GitOps acceptance", "remove_source_branch": True})
    state["deploy_mr"] = mr["iid"]
    save(path, state)
    print(f"created disposable deploy MR !{mr['iid']}", flush=True)
    return mr["iid"]


def ensure_removal_mr(gitlab, state, path):
    if state.get("removal_mr"):
        return state["removal_mr"]
    branch = state.setdefault("removal_branch", "test/gitops-remove-whoami-" + uuid.uuid4().hex[:12])
    save(path, state)
    ensure_branch(gitlab, branch)
    if not state.get("removal_committed"):
        if gitlab.file("services/whoami.yaml", branch) is None:
            raise RuntimeError("whoami is already absent before reviewed removal")
        gitlab.api("POST", gitlab.root + "/repository/commits", {"branch": branch, "commit_message": "test: remove disposable private web service", "actions": [{"action": "delete", "file_path": "services/whoami.yaml"}]})
        mark(state, path, "removal_committed")
    recovered = existing_mr(gitlab, branch)
    if recovered:
        state["removal_mr"] = recovered
        save(path, state)
        return recovered
    mr = gitlab.api("POST", gitlab.root + "/merge_requests", {"source_branch": branch, "target_branch": "main", "title": "Disposable GitOps service removal", "remove_source_branch": True})
    state["removal_mr"] = mr["iid"]
    save(path, state)
    print(f"created disposable removal MR !{mr['iid']}", flush=True)
    return mr["iid"]


def dns(name, present=True):
    try:
        address = socket.gethostbyname(name)
    except socket.gaierror as error:
        absent_errors = {value for value in (getattr(socket, "EAI_NONAME", None), getattr(socket, "EAI_NODATA", None)) if value is not None}
        if not present and error.errno in absent_errors:
            return
        reason = "did not resolve" if present else "lookup failed transiently while checking removal"
        raise RuntimeError(f"DNS {reason} for {name}") from None
    if not present:
        raise RuntimeError(f"removed service DNS still resolves {name} to {address}")
    if address != "10.0.2.15":
        raise RuntimeError(f"DNS resolved {name} to unexpected {address}")


def timer():
    output = subprocess.check_output(["sudo", "systemctl", "cat", "bootstrap-certificate-renew.timer"], text=True)
    if "RandomizedDelaySec=37m" not in output:
        raise RuntimeError("reviewed host timer did not converge")


def verify_deploy():
    dns("gitops-proof.lab.test")
    dns("whoami.lab.test")
    timer()
    subprocess.run(["sudo", "/usr/local/sbin/bootstrap-gitops-health"], check=True)
    health = json.loads(subprocess.check_output(["sudo", "cat", "/var/lib/bootstrap-home-infra/gitops-services/status.json"], text=True))
    whoami = health.get("services", {}).get("whoami", {})
    if not all(whoami.get(key) for key in ("https_auth_boundary", "application")):
        raise RuntimeError("service trusted TLS, authentication redirect, or internal health failed")
    print("core DNS, host timer, trusted TLS/authentication redirect, and internal health verified", flush=True)


def verify_installed_authentik(state, checkpoint, head_sha, source_fingerprint, evidence_path):
    pipeline_id, pipeline_sha = state.get("deploy_pipeline"), state.get("deploy_pipeline_sha")
    if not pipeline_id or not pipeline_sha:
        raise RuntimeError("deploy checkpoint lacks its protected-main pipeline ID/SHA")
    expected = {
        "test": "installed-authentik-allowed-denied-login",
        "status": "PASS",
        "source_sha": pipeline_sha,
        "source_fingerprint": source_fingerprint,
        "merge_head_sha": head_sha,
        "pipeline_id": pipeline_id,
        "pipeline_sha": pipeline_sha,
        "identities_cleaned": True,
    }
    bound = all(state.get("authentik_login_" + key) == value for key, value in (
        ("head_sha", head_sha), ("pipeline_id", pipeline_id), ("pipeline_sha", pipeline_sha),
        ("source_fingerprint", source_fingerprint)))
    evidence = None
    evidence_path = Path(evidence_path)
    if bound and evidence_path.exists():
        try:
            evidence = json.loads(evidence_path.read_text())
        except (OSError, json.JSONDecodeError):
            evidence = None
    outcomes = evidence.get("outcomes", {}) if isinstance(evidence, dict) else {}
    if (not isinstance(evidence, dict) or any(evidence.get(key) != value for key, value in expected.items())
            or outcomes.get("allowed_http_status") != 200 or not outcomes.get("allowed_reached_application")
            or outcomes.get("denied_http_status") != 403 or not outcomes.get("denied_forbidden")):
        command = [
            sys.executable, str(Path(__file__).with_name("test-gitops-guest-authentik.py")),
            "--source-sha", pipeline_sha, "--head-sha", head_sha,
            "--pipeline-id", str(pipeline_id), "--pipeline-sha", pipeline_sha,
            "--source-fingerprint", source_fingerprint,
            "--evidence", str(evidence_path),
        ]
        subprocess.run(command, check=True)
        try:
            evidence = json.loads(evidence_path.read_text())
        except (OSError, json.JSONDecodeError):
            raise RuntimeError("installed Authentik E2E did not produce readable evidence") from None
        outcomes = evidence.get("outcomes", {}) if isinstance(evidence, dict) else {}
        if (not isinstance(evidence, dict) or any(evidence.get(key) != value for key, value in expected.items())
                or outcomes.get("allowed_http_status") != 200 or not outcomes.get("allowed_reached_application")
                or outcomes.get("denied_http_status") != 403 or not outcomes.get("denied_forbidden")):
            raise RuntimeError("installed Authentik E2E evidence is incomplete or bound to another deploy")
    state["authentik_login_head_sha"] = head_sha
    state["authentik_login_pipeline_id"] = pipeline_id
    state["authentik_login_pipeline_sha"] = pipeline_sha
    state["authentik_login_source_fingerprint"] = source_fingerprint
    mark(state, checkpoint, "authentik_login_verified")


def verify_full_resume(state, checkpoint, source_fingerprint, evidence_path="logs/release-check/full-resume.json"):
    deploy_sha = state.get("deploy_pipeline_sha")
    if not deploy_sha:
        raise RuntimeError("deploy checkpoint lacks its applied source SHA for resume verification")
    evidence_path = Path(evidence_path)
    expected = {
        "schema": 1,
        "status": "PASS",
        "source_fingerprint": source_fingerprint,
        "deploy_sha": deploy_sha,
        "state_preserved": True,
        "secrets_preserved": True,
        "cancel_observed": True,
    }
    bound = state.get("full_resume_source_fingerprint") == source_fingerprint
    try:
        evidence = json.loads(evidence_path.read_text()) if bound and evidence_path.exists() else None
    except (OSError, json.JSONDecodeError):
        evidence = None
    if (not isinstance(evidence, dict) or any(evidence.get(key) != value for key, value in expected.items())
            or not evidence.get("interrupted_pipeline_id") or not evidence.get("retry_pipeline_id")
            or evidence.get("interrupted_pipeline_sha") != deploy_sha
            or evidence.get("retry_pipeline_sha") != deploy_sha
            or not isinstance(evidence.get("states_compared"), int) or evidence["states_compared"] < 1):
        command = [sys.executable, str(Path(__file__).with_name("test-gitops-guest-resume.py")),
                   "--source-fingerprint", source_fingerprint]
        subprocess.run(command, check=True)
        try:
            evidence = json.loads(evidence_path.read_text())
        except (OSError, json.JSONDecodeError):
            raise RuntimeError("full-resume drill did not produce readable evidence") from None
        if (not isinstance(evidence, dict) or any(evidence.get(key) != value for key, value in expected.items())
                or not evidence.get("interrupted_pipeline_id") or not evidence.get("retry_pipeline_id")
                or evidence.get("interrupted_pipeline_sha") != deploy_sha
                or evidence.get("retry_pipeline_sha") != deploy_sha
                or not isinstance(evidence.get("states_compared"), int) or evidence["states_compared"] < 1):
            raise RuntimeError("full-resume evidence is incomplete or bound to another deploy")
    state["full_resume_source_fingerprint"] = source_fingerprint
    state["full_resume_pipeline_sha"] = deploy_sha
    mark(state, checkpoint, "full_resume_verified")


def no_change(gitlab, label="no-change"):
    print(f"rerunning protected main without changes ({label})", flush=True)
    pipeline = gitlab.api("POST", gitlab.root + "/pipeline", {"ref": "main"})
    wait_pipeline(gitlab, pipeline["id"])
    jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline['id']}/jobs")
    reconcile = next((job for job in jobs if job["name"] == "reconcile"), None)
    if reconcile is None:
        raise RuntimeError("no-change pipeline did not run reconcile")
    trace = gitlab.api("GET", gitlab.root + f"/jobs/{reconcile['id']}/trace", raw=True)
    if MUTATION_SUMMARY.search(trace):
        raise RuntimeError("no-change rerun emitted Terraform mutation summaries")
    print(f"{label} rerun completed with no Terraform mutation summaries", flush=True)


def verify_authentik_removal(gitlab, pipeline_id):
    jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}/jobs")
    reconcile = next((job for job in jobs if job["name"] == "reconcile"), None)
    if reconcile is None:
        raise RuntimeError("removal pipeline did not run reconcile")
    trace = gitlab.api("GET", gitlab.root + f"/jobs/{reconcile['id']}/trace", raw=True)
    # The runner prints only resource address/action summaries, never state or
    # provider output. This proves the reviewed Authentik application teardown.
    if not re.search(r'(?m)^bootstrap-authentik: delete .*authentik_(?:application|provider_oauth2).*whoami', trace):
        raise RuntimeError("removal pipeline did not report Authentik whoami teardown")


def verify_removal(gitlab, pipeline_id):
    verify_authentik_removal(gitlab, pipeline_id)
    dns("whoami.lab.test", present=False)
    route_absent()
    releases = subprocess.check_output(["sudo", "/usr/local/bin/k3s", "kubectl", "--kubeconfig", "/etc/bootstrap-home-infra/gitops-kubeconfig-bootstrap", "-n", "service-whoami", "get", "secret", "-o", "name"], text=True, stderr=subprocess.DEVNULL)
    if any(".helm.release.v1.whoami" in line or ".helm.release.v1.whoami-access" in line for line in releases.splitlines()):
        raise RuntimeError("removed service still has a Helm release")
    subprocess.run(["sudo", "/usr/local/sbin/bootstrap-gitops-health"], check=True)
    health = json.loads(subprocess.check_output(["sudo", "cat", "/var/lib/bootstrap-home-infra/gitops-services/status.json"], text=True))
    if health.get("services"):
        raise RuntimeError("removed service remains in host health intent")
    dns("gitops-proof.lab.test")
    dns("gitlab.lab.test")
    timer()
    print("reviewed deletion removed DNS, Helm releases, and host route; retained core is healthy", flush=True)


def route_absent():
    route = subprocess.run(["sudo", "grep", "-F", "whoami.lab.test", "/etc/nginx/conf.d/bootstrap-gitops-services.conf"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    if route.returncode == 0:
        raise RuntimeError("removed service still has a host nginx route")
    if route.returncode != 1:
        raise RuntimeError(f"could not check the removed service host route (grep exited {route.returncode})")


def run(args):
    require_guest()
    token = (Path.home() / ".bootstrap/gitlab-api-token").read_text().strip()
    if not token:
        raise RuntimeError("disposable GitLab API token is empty")
    checkpoint, state = Path(args.checkpoint), load(Path(args.checkpoint))
    if args.resume_mr is not None:
        if state.get("deploy_mr") and state["deploy_mr"] != args.resume_mr:
            raise RuntimeError("checkpoint belongs to a different deploy MR")
        state["deploy_mr"] = args.resume_mr
        save(checkpoint, state)
    gitlab = GitLab(token)
    deploy = ensure_deploy_mr(gitlab, state, checkpoint)
    deploy_sha = gitlab.mr(deploy)["sha"]
    bind_checkpoint(state, checkpoint, "deploy_head_sha", deploy_sha,
                    ("deploy_plan", "deploy_plan_sha", "deploy_apply", "deploy_pipeline", "deploy_pipeline_sha",
                     "deploy_verified", "authentik_login_verified", "full_resume_verified", "no_change_verified"))
    if not state.get("deploy_plan") or state.get("deploy_plan_sha") != deploy_sha:
        plan_sha = ensure_mr_plan(gitlab, deploy)
        state["deploy_plan_sha"] = plan_sha
        mark(state, checkpoint, "deploy_plan")
    if not state.get("deploy_apply"):
        pipeline_id, pipeline_sha = merge_and_apply(gitlab, deploy, state.get("deploy_plan_sha"))
        state["deploy_pipeline"], state["deploy_pipeline_sha"] = pipeline_id, pipeline_sha
        mark(state, checkpoint, "deploy_apply")
    if not state.get("deploy_verified"):
        verify_deploy(); mark(state, checkpoint, "deploy_verified")
    verify_installed_authentik(state, checkpoint, deploy_sha, args.source_fingerprint, args.authentik_evidence)
    verify_full_resume(state, checkpoint, args.source_fingerprint)
    if not state.get("no_change_verified"):
        no_change(gitlab); mark(state, checkpoint, "no_change_verified")
    removal = ensure_removal_mr(gitlab, state, checkpoint)
    removal_sha = gitlab.mr(removal)["sha"]
    bind_checkpoint(state, checkpoint, "removal_head_sha", removal_sha,
                    ("removal_plan", "removal_plan_sha", "removal_apply", "removal_pipeline",
                     "removal_pipeline_sha", "removal_verified", "removal_rerun_verified"))
    if not state.get("removal_plan") or state.get("removal_plan_sha") != removal_sha:
        plan_sha = ensure_mr_plan(gitlab, removal)
        state["removal_plan_sha"] = plan_sha
        mark(state, checkpoint, "removal_plan")
    if not state.get("removal_apply"):
        pipeline_id, pipeline_sha = merge_and_apply(gitlab, removal, state.get("removal_plan_sha"))
        state["removal_pipeline"], state["removal_pipeline_sha"] = pipeline_id, pipeline_sha
        mark(state, checkpoint, "removal_apply")
    if not state.get("removal_verified"):
        pipeline = state.get("removal_pipeline")
        if not pipeline or not state.get("removal_pipeline_sha"):
            raise RuntimeError("removal checkpoint lacks its reconciliation pipeline ID/SHA")
        verify_removal(gitlab, pipeline); mark(state, checkpoint, "removal_verified")
    if not state.get("removal_rerun_verified"):
        no_change(gitlab, "post-removal")
        verify_removal(gitlab, state["removal_pipeline"])
        mark(state, checkpoint, "removal_rerun_verified")
    print("disposable GitLab deploy, installed-Authentik allow/deny login, rerun, and reviewed removal acceptance passed", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--resume-mr", type=int, help="recover the deploy MR after losing its checkpoint")
    parser.add_argument("--checkpoint", default=str(CHECKPOINT), help="guest-only ignored checkpoint path")
    parser.add_argument("--authentik-evidence", default="logs/release-check/authentik-login.json",
                        help="guest-only sanitized installed-Authentik E2E evidence path")
    parser.add_argument("--source-fingerprint", required=True,
                        help="release runner fingerprint for the source tree synced to this guest")
    run(parser.parse_args())
