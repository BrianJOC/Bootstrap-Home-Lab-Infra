#!/usr/bin/env python3
"""Compare seeded GitLab source files to the exact source synchronized by release-check."""
import base64
import hashlib
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
BASE = "https://gitlab.lab.test/api/v4"
PROJECT_PATH = "infrastructure%2Fiac"
EVIDENCE = ROOT / "logs/release-check/seed-source-binding.json"
REQUIRED = {
    ".gitlab-ci.yml": "gitops/gitlab-ci.yml",
    "cmd/gitops/main.go": "cmd/gitops/main.go",
    "internal/gitops/runner.go": "internal/gitops/runner.go",
    "terraform/modules/web-service/main.tf": "terraform/modules/web-service/main.tf",
    "gitops/examples/whoami.yaml": "gitops/examples/whoami.yaml",
}


class GitLab:
    def __init__(self, token):
        self.token = token
        self.project_id = None

    def get(self, path):
        request = urllib.request.Request(BASE + path, headers={"PRIVATE-TOKEN": self.token})
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.load(response)

    @property
    def root(self):
        if self.project_id is None:
            self.project_id = self.get("/projects/" + PROJECT_PATH)["id"]
        return f"/projects/{self.project_id}"

    def file(self, path, ref="main"):
        encoded = urllib.parse.quote(path, safe="")
        item = self.get(self.root + "/repository/files/" + encoded + "?ref=" + urllib.parse.quote(ref, safe=""))
        return base64.b64decode(item["content"])


def write_evidence(record):
    EVIDENCE.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = EVIDENCE.with_suffix(".tmp")
    temporary.write_text(json.dumps(record, sort_keys=True, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(EVIDENCE)


def run():
    manifest = json.loads((ROOT / "test/release-source.json").read_text())
    if manifest.get("schema") != 1 or not isinstance(manifest.get("source"), str):
        raise RuntimeError("release source manifest is invalid")
    token = (Path.home() / ".bootstrap/gitlab-api-token").read_text().strip()
    if not token:
        raise RuntimeError("disposable GitLab API token is empty")
    gitlab = GitLab(token)
    seed_files = json.loads(gitlab.file("platform/seed-files.json", "main"))
    if not isinstance(seed_files, dict):
        raise RuntimeError("GitLab seed file manifest is invalid")
    expected = manifest["files"]
    comparisons = 0
    for path in sorted(seed_files):
        # Seed-generated platform configuration and the seed manifest itself
        # have no same-path source file. Compare only source-bound entries.
        if path not in expected:
            continue
        try:
            actual = gitlab.file(path, "main")
        except urllib.error.HTTPError as error:
            raise RuntimeError("a manifest-bound GitLab source file is missing") from None if error.code == 404 else error
        if hashlib.sha256(actual).hexdigest() != expected[path]:
            raise RuntimeError("GitLab contains source that differs from the synchronized checkout")
        comparisons += 1
    # SeedProject deliberately relocates these source inputs. Require those
    # essential files explicitly so an old or partial seed cannot pass by
    # matching only unrelated infrastructure files.
    for remote, local in REQUIRED.items():
        if local not in expected:
            raise RuntimeError("the synchronized source manifest lacks a required GitOps file")
        actual = gitlab.file(remote, "main")
        if hashlib.sha256(actual).hexdigest() != expected[local]:
            raise RuntimeError("a required GitLab GitOps source file differs from this checkout")
        comparisons += 1
    if comparisons < 25:
        raise RuntimeError("too few seeded source files matched the synchronized checkout")
    main = gitlab.get(gitlab.root + "/repository/branches/main")
    write_evidence({"schema": 1, "status": "PASS", "source_fingerprint": manifest["source"],
                    "main_sha": main["commit"]["id"], "matched_files": comparisons})
    print("seeded source binding verified", flush=True)


if __name__ == "__main__":
    try:
        run()
    except (OSError, ValueError, RuntimeError, urllib.error.URLError, KeyError, TypeError):
        write_evidence({"schema": 1, "status": "FAIL"})
        raise SystemExit("seeded source binding failed; details are intentionally omitted")
