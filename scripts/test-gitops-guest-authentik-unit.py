#!/usr/bin/env python3
"""Stdlib unit checks for installed-Authentik login acceptance."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("test-gitops-guest-authentik.py")
SPEC = importlib.util.spec_from_file_location("gitops_guest_authentik", SCRIPT)
fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fixture)


class FakeResponse:
    def __init__(self, status, url, body):
        self.status, self.url, self.body = status, url, body

    def geturl(self):
        return self.url

    def read(self):
        return self.body


class FakeOpener:
    def __init__(self, responses):
        self.responses = list(responses)
        self.requests = []

    def open(self, request, timeout):
        self.requests.append(request)
        response = self.responses.pop(0)
        return FakeResponse(*response)


class FlowTests(unittest.TestCase):
    def test_real_flow_protocol_uses_identification_password_and_redirect(self):
        opener = FakeOpener([
            (200, "https://authentik.lab.test/api/v3/flows/executor/default-authentication-flow/", b'{"component":"ak-stage-identification"}'),
            (200, "https://authentik.lab.test/api/v3/flows/executor/default-authentication-flow/", b'{"component":"ak-stage-password"}'),
            (200, "https://authentik.lab.test/api/v3/flows/executor/default-authentication-flow/", b'{"component":"xak-flow-redirect","to":"/application/o/authorize/?state=synthetic"}'),
        ])
        target = fixture.authentik_flow(
            opener,
            "https://authentik.lab.test/if/flow/default-authentication-flow/?next=%2Fapplication%2Fo%2Fauthorize%2F",
            "synthetic-user", "synthetic-password",
        )
        self.assertEqual(target, "https://authentik.lab.test/application/o/authorize/?state=synthetic")
        self.assertEqual(len(opener.requests), 3)
        self.assertEqual(json.loads(opener.requests[1].data), {
            "component": "ak-stage-identification", "uid_field": "synthetic-user"})
        self.assertEqual(json.loads(opener.requests[2].data), {
            "component": "ak-stage-password", "password": "synthetic-password"})
        self.assertNotIn("synthetic-password", opener.requests[2].full_url)

    def test_unknown_authentication_step_fails_closed(self):
        opener = FakeOpener([
            (200, "https://authentik.lab.test/api/v3/flows/executor/default-authentication-flow/",
             b'{"component":"ak-stage-authenticator-totp"}'),
        ])
        with self.assertRaisesRegex(fixture.CheckFailure, "unsupported authentication step"):
            fixture.authentik_flow(opener, "https://authentik.lab.test/if/flow/default-authentication-flow/",
                                   "synthetic-user", "synthetic-password")


class EvidenceTests(unittest.TestCase):
    def test_pass_requires_allowed_application_and_denied_403(self):
        with tempfile.TemporaryDirectory() as directory:
            token_file = Path(directory) / "token"
            token_file.write_text("synthetic-api-token")
            evidence_file = Path(directory) / "evidence.json"
            args = fixture.argparse.Namespace(
                source_sha="a" * 40, head_sha="b" * 40,
                pipeline_id=31, pipeline_sha="a" * 40, source_fingerprint="c" * 64,
                evidence=str(evidence_file),
            )
            def identities(token, nonce, password, records):
                records["users"] = [101, 102]
                records["groups"] = ["synthetic-group"]
            with (
                mock.patch.object(fixture, "require_guest"),
                mock.patch.object(fixture, "TOKEN_FILE", token_file),
                mock.patch.object(fixture, "create_identities", side_effect=identities),
                mock.patch.object(fixture, "cleanup_identities") as cleanup,
                mock.patch.object(fixture, "login_as", side_effect=[
                    (200, "https://whoami.lab.test/", b"Hostname: whoami-123"),
                    (403, "https://whoami.lab.test/oauth2/callback", b""),
                ]),
            ):
                fixture.run(args)
            cleanup.assert_called_once_with("synthetic-api-token", {"users": [101, 102], "groups": ["synthetic-group"]})
            record = json.loads(evidence_file.read_text())
            self.assertEqual(record["status"], "PASS")
            self.assertEqual(record["source_sha"], "a" * 40)
            self.assertEqual(record["source_fingerprint"], "c" * 64)
            self.assertEqual(record["pipeline_id"], 31)
            self.assertTrue(record["outcomes"]["allowed_reached_application"])
            self.assertTrue(record["outcomes"]["denied_forbidden"])
            self.assertNotIn("username", json.dumps(record))
            self.assertEqual(evidence_file.stat().st_mode & 0o777, 0o600)

    def test_denied_user_reaching_application_fails_and_cleans_up(self):
        with tempfile.TemporaryDirectory() as directory:
            token_file = Path(directory) / "token"
            token_file.write_text("synthetic-api-token")
            evidence_file = Path(directory) / "evidence.json"
            args = fixture.argparse.Namespace(
                source_sha="a" * 40, head_sha="b" * 40,
                pipeline_id=31, pipeline_sha="a" * 40, source_fingerprint="c" * 64,
                evidence=str(evidence_file),
            )
            def identities(token, nonce, password, records):
                records["users"] = [101, 102]
                records["groups"] = ["synthetic-group"]
            with (
                mock.patch.object(fixture, "require_guest"),
                mock.patch.object(fixture, "TOKEN_FILE", token_file),
                mock.patch.object(fixture, "create_identities", side_effect=identities),
                mock.patch.object(fixture, "cleanup_identities") as cleanup,
                mock.patch.object(fixture, "login_as", side_effect=[
                    (200, "https://whoami.lab.test/", b"Hostname: whoami-123"),
                    (200, "https://whoami.lab.test/", b"Hostname: whoami-456"),
                ]),
            ):
                with self.assertRaisesRegex(fixture.CheckFailure, "was not rejected"):
                    fixture.run(args)
            cleanup.assert_called_once()
            record = json.loads(evidence_file.read_text())
            self.assertEqual(record["status"], "FAIL")
            self.assertFalse(record["outcomes"]["denied_forbidden"])


if __name__ == "__main__":
    unittest.main()
