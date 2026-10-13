#!/usr/bin/env python3
"""Interrupt and recover a no-change protected-main GitLab reconciliation."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import socket
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "https://gitlab.lab.test/api/v4"
PROJECT_PATH = "infrastructure%2Fiac"
ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "logs/release-check/full-resume.json"


class GitLab:
    def __init__(self, token):
        self.token, self.project = token, None

    def api(self, method, path, body=None, raw=False):
        payload = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(BASE + path, data=payload, method=method,
                                         headers={"PRIVATE-TOKEN": self.token,
                                                  "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=90) as response:
                return response.read() if raw else (json.load(response) if response.status != 204 else None)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"GitLab API returned HTTP {error.code}") from None
        except urllib.error.URLError:
            raise RuntimeError("GitLab API could not be reached") from None

    @property
    def root(self):
        if self.project is None:
            self.project = self.api("GET", "/projects/" + PROJECT_PATH)["id"]
        return f"/projects/{self.project}"


def save(record):
    EVIDENCE.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = EVIDENCE.with_suffix(".tmp")
    temporary.write_text(json.dumps(record, sort_keys=True, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(EVIDENCE)


def digest(value):
    return hashlib.sha256(value).hexdigest()


def private_files():
    paths = [Path.home() / ".bootstrap/gitlab-api-token", Path("/home/ansible/gitops-test-vault-init.json")]
    result = {}
    for path in paths:
        data = path.read_bytes()
        if not data:
            raise RuntimeError("a required private fixture file is empty")
        result[str(path)] = digest(data)
    return result


def file_json(gitlab, path):
    encoded = urllib.parse.quote(path, safe="")
    item = gitlab.api("GET", gitlab.root + "/repository/files/" + encoded + "?ref=main")
    return json.loads(base64.b64decode(item["content"]))


def state_snapshot(gitlab):
    platform = file_json(gitlab, "platform/platform.json")
    roots = platform.get("roots")
    if not isinstance(roots, list) or not roots:
        raise RuntimeError("GitLab platform manifest has no Terraform states")
    snapshot = {}
    for root in roots:
        name = root.get("state")
        if not isinstance(name, str) or not name:
            raise RuntimeError("GitLab platform state name is invalid")
        raw = gitlab.api("GET", gitlab.root + "/terraform/state/" + urllib.parse.quote(name, safe=""), raw=True)
        try:
            state = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError):
            raise RuntimeError("GitLab returned an invalid Terraform state") from None
        resources = {"resources": state.get("resources", []), "outputs": state.get("outputs", {})}
        snapshot[name] = {"lineage": state.get("lineage"), "serial": state.get("serial"),
                          "content": digest(json.dumps(resources, sort_keys=True, separators=(",", ":")).encode())}
        if not snapshot[name]["lineage"] or not isinstance(snapshot[name]["serial"], int):
            raise RuntimeError("Terraform state is missing lineage or serial")
    return snapshot


def wait_for_reconcile(gitlab, pipeline_id, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        jobs = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}/jobs")
        reconcile = next((job for job in jobs if job.get("name") == "reconcile"), None)
        if reconcile and reconcile["status"] == "running":
            return reconcile
        pipeline = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}")
        if pipeline["status"] in ("failed", "canceled", "skipped", "success"):
            raise RuntimeError("protected-main reconcile ended before the interruption point")
        time.sleep(3)
    raise RuntimeError("protected-main reconcile did not start within the bounded wait")


def wait_pipeline(gitlab, pipeline_id, timeout=3600):
    deadline, previous = time.monotonic() + timeout, None
    while time.monotonic() < deadline:
        pipeline = gitlab.api("GET", gitlab.root + f"/pipelines/{pipeline_id}")
        status = pipeline["status"]
        if status == "success":
            return pipeline
        if status in ("failed", "canceled", "skipped", "manual"):
            raise RuntimeError("retry protected-main pipeline did not succeed")
        if status != previous:
            previous = status
        time.sleep(4)
    raise RuntimeError("retry protected-main pipeline exceeded its time budget")


def run(args):
    if socket.gethostname() != "bootstrap-test" or Path.home() != Path("/home/ansible"):
        raise RuntimeError("resume drill is restricted to the disposable QEMU guest")
    manifest = json.loads((ROOT / "test/release-source.json").read_text())
    if manifest.get("schema") != 1 or manifest.get("source") != args.source_fingerprint:
        raise RuntimeError("resume drill source fingerprint does not match synchronized source")
    if args.deploy_sha and not all(c in "0123456789abcdef" for c in args.deploy_sha.lower()):
        raise RuntimeError("deploy commit SHA is invalid")
    token = (Path.home() / ".bootstrap/gitlab-api-token").read_text().strip()
    if not token:
        raise RuntimeError("disposable GitLab API token is empty")
    gitlab = GitLab(token)
    main = gitlab.api("GET", gitlab.root + "/repository/branches/main")["commit"]["id"]
    if args.deploy_sha and main != args.deploy_sha:
        raise RuntimeError("protected main moved after the deploy checkpoint")
    private_before = private_files()
    state_before = state_snapshot(gitlab)

    interrupted = gitlab.api("POST", gitlab.root + "/pipeline", {"ref": "main"})
    if interrupted.get("sha") != main:
        raise RuntimeError("interruption pipeline is not bound to current protected main")
    job = wait_for_reconcile(gitlab, interrupted["id"], 300)
    # The job must be observed running before cancellation. This is a no-change
    # main pipeline after the deploy fixture has already converged.
    gitlab.api("POST", gitlab.root + f"/jobs/{job['id']}/cancel")
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        current = gitlab.api("GET", gitlab.root + f"/pipelines/{interrupted['id']}")
        if current["status"] in ("canceled", "failed"):
            break
        if current["status"] == "success":
            raise RuntimeError("reconcile completed before cancellation was observed")
        time.sleep(3)
    else:
        raise RuntimeError("interrupted pipeline did not reach a stopped state")
    interrupted_status = current["status"]

    retry = gitlab.api("POST", gitlab.root + "/pipeline", {"ref": "main"})
    if retry.get("sha") != main:
        raise RuntimeError("retry pipeline is not bound to the same protected-main commit")
    completed = wait_pipeline(gitlab, retry["id"])
    private_after = private_files()
    state_after = state_snapshot(gitlab)
    states_preserved = (state_before.keys() == state_after.keys() and all(
        state_before[name]["lineage"] == state_after[name]["lineage"]
        and state_before[name]["serial"] <= state_after[name]["serial"]
        and state_before[name]["content"] == state_after[name]["content"]
        for name in state_before))
    secrets_preserved = private_before == private_after
    record = {"schema": 1, "status": "PASS" if states_preserved and secrets_preserved else "FAIL",
              "source_fingerprint": args.source_fingerprint, "deploy_sha": main,
              "interrupted_pipeline_id": interrupted["id"], "interrupted_pipeline_sha": interrupted["sha"],
              "interrupted_pipeline_status": interrupted_status,
              "retry_pipeline_id": retry["id"], "retry_pipeline_sha": completed["sha"],
              "state_preserved": states_preserved, "secrets_preserved": secrets_preserved,
              "cancel_observed": True, "states_compared": len(state_before)}
    save(record)
    if record["status"] != "PASS":
        raise RuntimeError("state or private fixture values changed during protected-main recovery")
    print("protected-main interruption and recovery verified", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-fingerprint", required=True)
    parser.add_argument("--deploy-sha", default="", help="expected protected-main SHA after deploy")
    arguments = parser.parse_args()
    try:
        run(arguments)
    except (OSError, ValueError, RuntimeError, KeyError, TypeError):
        # Do not include an exception string: it may contain service or provider data.
        save({"schema": 1, "status": "FAIL", "source_fingerprint": arguments.source_fingerprint,
              "state_preserved": False, "secrets_preserved": False, "cancel_observed": False})
        raise SystemExit("protected-main recovery drill failed; details are intentionally omitted")
