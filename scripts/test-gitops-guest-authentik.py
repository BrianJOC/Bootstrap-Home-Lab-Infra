#!/usr/bin/env python3
"""Exercise installed Authentik users through the deployed whoami OAuth gateway.

Creates disposable synthetic users and a denied-only group, performs the real
OAuth2 Proxy authorization-code flow using Authentik's supported flow executor,
and removes the test identities. Evidence contains no credentials or identities.
"""
import argparse
from datetime import datetime, timezone
import http.cookiejar
import json
from pathlib import Path
import re
import secrets
import socket
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid

AUTHENTIK = "https://authentik.lab.test"
APP = "https://whoami.lab.test/"
TOKEN_FILE = Path.home() / ".bootstrap/authentik-bootstrap-token"


class CheckFailure(RuntimeError):
    """A failure with a fixed, credential-free category."""


def require_guest():
    if socket.gethostname() != "bootstrap-test" or Path.home() != Path("/home/ansible"):
        raise SystemExit("this fixture only runs as ansible inside the disposable QEMU guest")


def write_evidence(path, evidence):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def api(token, method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(
        AUTHENTIK + "/api/v3" + path,
        data=data,
        method=method,
        headers={"Authorization": "Bearer " + token, "Accept": "application/json",
                 "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read()
            return None if response.status == 204 else json.loads(raw)
    except urllib.error.HTTPError as error:
        raise CheckFailure("Authentik synthetic identity API rejected a test operation") from None
    except (urllib.error.URLError, TimeoutError, OSError):
        raise CheckFailure("Authentik synthetic identity API could not be reached") from None


def first_page_results(document):
    if not isinstance(document, dict) or not isinstance(document.get("results"), list):
        raise CheckFailure("Authentik identity API returned an unexpected response")
    return document["results"]


def create_identities(token, nonce, password, records):
    groups = first_page_results(api(token, "GET", "/core/groups/?search=adults&page_size=100"))
    allowed_group = next((group for group in groups if group.get("name") == "adults"), None)
    if not allowed_group:
        raise CheckFailure("the deployed service allow group is missing from Authentik")

    denied_group = api(token, "POST", "/core/groups/", {
        "name": "release-e2e-" + nonce + "-denied", "is_superuser": False,
    })
    records["groups"].append(denied_group["pk"])
    allowed = api(token, "POST", "/core/users/", {
        "username": "release-e2e-" + nonce + "-allowed",
        "name": "Disposable release test allowed user",
        "email": "release-e2e-" + nonce + "-allowed@example.invalid",
        "is_active": True, "type": "internal", "groups": [allowed_group["pk"]],
    })
    records["users"].append(allowed["pk"])
    api(token, "POST", f"/core/users/{allowed['pk']}/set_password/", {"password": password})

    denied = api(token, "POST", "/core/users/", {
        "username": "release-e2e-" + nonce + "-denied",
        "name": "Disposable release test denied user",
        "email": "release-e2e-" + nonce + "-denied@example.invalid",
        "is_active": True, "type": "internal", "groups": [denied_group["pk"]],
    })
    records["users"].append(denied["pk"])
    api(token, "POST", f"/core/users/{denied['pk']}/set_password/", {"password": password})
    return records, password


def cleanup_identities(token, records):
    errors = []
    for user_id in reversed(records.get("users", [])):
        try:
            api(token, "DELETE", f"/core/users/{user_id}/")
        except CheckFailure:
            errors.append("user")
    for group_id in reversed(records.get("groups", [])):
        try:
            api(token, "DELETE", f"/core/groups/{group_id}/")
        except CheckFailure:
            errors.append("group")
    if errors:
        raise CheckFailure("Authentik synthetic test identities could not all be cleaned up")


def open_response(opener, url, data=None, headers=None):
    request = urllib.request.Request(url, data=data, headers=headers or {})
    try:
        response = opener.open(request, timeout=45)
        return response.status, response.geturl(), response.read()
    except urllib.error.HTTPError as error:
        # Read and discard the body; it may contain provider diagnostics or
        # identity data and is never suitable for local evidence.
        error.read()
        return error.code, error.geturl(), b""
    except (urllib.error.URLError, TimeoutError, OSError):
        raise CheckFailure("the deployed OAuth login flow could not be reached") from None


def authentik_flow(opener, login_url, username, password):
    parsed = urllib.parse.urlsplit(login_url)
    flow_match = re.fullmatch(r"/if/flow/([A-Za-z0-9_-]+)/?", parsed.path)
    if parsed.hostname != "authentik.lab.test" or not flow_match:
        raise CheckFailure("the gateway did not redirect to an Authentik authentication flow")
    slug = flow_match.group(1)
    query = urllib.parse.urlencode({"query": parsed.query})
    endpoint = f"{AUTHENTIK}/api/v3/flows/executor/{slug}/?{query}"
    status, _, body = open_response(opener, endpoint, headers={"Accept": "application/json"})
    if status != 200:
        raise CheckFailure("Authentik did not start the interactive authentication flow")
    challenge = decode_challenge(body)
    for _ in range(8):
        component = challenge.get("component")
        if component == "ak-stage-identification":
            response = {"component": component, "uid_field": username}
        elif component == "ak-stage-password":
            response = {"component": component, "password": password}
        elif component == "xak-flow-redirect":
            target = challenge.get("to")
            if not isinstance(target, str) or not target:
                raise CheckFailure("Authentik completed login without a redirect target")
            return urllib.parse.urljoin(AUTHENTIK + "/", target)
        elif component == "ak-stage-access-denied":
            raise CheckFailure("Authentik denied a synthetic user during authentication")
        else:
            # Fail closed on MFA or any unexpected step; never call an account
            # with an unknown challenge a successful login.
            raise CheckFailure("Authentik presented an unsupported authentication step")
        status, _, body = open_response(
            opener, endpoint, json.dumps(response).encode(),
            {"Accept": "application/json", "Content-Type": "application/json"},
        )
        if status not in (200, 302):
            raise CheckFailure("Authentik rejected a synthetic test login")
        if status == 302 or not body:
            # Authentik can redirect to the same executor between stages. The
            # opener follows that redirect and returns its next JSON challenge.
            raise CheckFailure("Authentik returned an empty flow response")
        challenge = decode_challenge(body)
    raise CheckFailure("Authentik authentication exceeded its stage limit")


def decode_challenge(body):
    try:
        value = json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise CheckFailure("Authentik returned an invalid flow challenge") from None
    if not isinstance(value, dict) or not isinstance(value.get("component"), str):
        raise CheckFailure("Authentik returned an invalid flow challenge")
    return value


def login_as(username, password):
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status, login_url, _ = open_response(opener, APP)
    if status != 200 or urllib.parse.urlsplit(login_url).hostname != "authentik.lab.test":
        raise CheckFailure("the deployed service did not start its Authentik login redirect")
    continuation = authentik_flow(opener, login_url, username, password)
    return open_response(opener, continuation)


def run(args):
    require_guest()
    if not re.fullmatch(r"[0-9a-f]{7,64}", args.source_sha):
        raise CheckFailure("source SHA is missing or malformed")
    source_fingerprint = getattr(args, "source_fingerprint", None)
    if not re.fullmatch(r"[0-9a-f]{64}", source_fingerprint):
        raise CheckFailure("source-content fingerprint is missing or malformed")
    if not re.fullmatch(r"[0-9a-f]{7,64}", args.pipeline_sha) or args.pipeline_sha != args.source_sha:
        raise CheckFailure("main pipeline SHA does not match the applied source SHA")
    if args.pipeline_id <= 0:
        raise CheckFailure("main pipeline ID is invalid")
    token = TOKEN_FILE.read_text().strip()
    if not token:
        raise CheckFailure("synthetic Authentik administration token is empty")
    nonce = uuid.uuid4().hex[:12]
    password = secrets.token_urlsafe(24)
    records = {"users": [], "groups": []}
    evidence = {
        "schema": 1,
        "test": "installed-authentik-allowed-denied-login",
        "status": "RUNNING",
        "source_sha": args.source_sha,
        "source_fingerprint": source_fingerprint,
        "merge_head_sha": args.head_sha,
        "pipeline_id": args.pipeline_id,
        "pipeline_sha": args.pipeline_sha,
        "outcomes": {"allowed_http_status": None, "allowed_reached_application": False,
                      "denied_http_status": None, "denied_forbidden": False},
        "identities_cleaned": False,
    }
    failure = None
    try:
        create_identities(token, nonce, password, records)
        allowed_name = "release-e2e-" + nonce + "-allowed"
        denied_name = "release-e2e-" + nonce + "-denied"
        allowed_status, allowed_url, allowed_body = login_as(allowed_name, password)
        evidence["outcomes"]["allowed_http_status"] = allowed_status
        allowed_host = urllib.parse.urlsplit(allowed_url).hostname
        evidence["outcomes"]["allowed_reached_application"] = (
            allowed_status == 200 and allowed_host == "whoami.lab.test"
            and b"Hostname: whoami-" in allowed_body
        )
        if not evidence["outcomes"]["allowed_reached_application"]:
            raise CheckFailure("allowed Authentik user did not reach the deployed whoami application")

        denied_status, denied_url, _ = login_as(denied_name, password)
        evidence["outcomes"]["denied_http_status"] = denied_status
        evidence["outcomes"]["denied_forbidden"] = (
            denied_status == 403 and urllib.parse.urlsplit(denied_url).hostname == "whoami.lab.test"
            and urllib.parse.urlsplit(denied_url).path == "/oauth2/callback"
        )
        if not evidence["outcomes"]["denied_forbidden"]:
            raise CheckFailure("denied Authentik user was not rejected by the deployed access gateway")
    except CheckFailure as error:
        failure = error
    finally:
        try:
            cleanup_identities(token, records)
            evidence["identities_cleaned"] = True
        except CheckFailure as error:
            failure = error

    evidence["status"] = "FAIL" if failure else "PASS"
    evidence["checked_at"] = datetime.now(timezone.utc).isoformat()
    write_evidence(Path(args.evidence), evidence)
    if failure:
        raise failure
    print(f"installed Authentik E2E passed: allowed=200 denied=403 pipeline={args.pipeline_id} sha={args.pipeline_sha}", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-sha", required=True, help="merged GitOps source commit SHA")
    parser.add_argument("--head-sha", required=True, help="source MR head SHA that was planned")
    parser.add_argument("--pipeline-id", required=True, type=int, help="successful protected-main pipeline ID")
    parser.add_argument("--pipeline-sha", required=True, help="source SHA applied by the protected-main pipeline")
    parser.add_argument("--source-fingerprint", required=True, help="local release source-content fingerprint")
    parser.add_argument("--evidence", default="logs/release-check/authentik-login.json")
    try:
        run(parser.parse_args())
    except CheckFailure as error:
        print(f"installed Authentik E2E failed: {error}", file=sys.stderr, flush=True)
        raise SystemExit(1) from None
