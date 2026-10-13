#!/usr/bin/env python3
"""Focused stdlib regression checks for the disposable GitOps acceptance fixture."""
import importlib.util
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("test-gitops-guest-merge.py")
SPEC = importlib.util.spec_from_file_location("gitops_guest_merge", SCRIPT)
fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fixture)


class FakeGitLab:
    root = ""

    def __init__(self, mr=None, responses=None):
        self.mr_value = mr or {"iid": 12, "sha": "head-sha", "state": "opened", "target_branch": "main"}
        self.responses = responses or {}
        self.calls = []

    def mr(self, iid):
        return self.mr_value

    def api(self, method, path, body=None, raw=False):
        self.calls.append((method, path, body))
        value = self.responses.get((method, path))
        if callable(value):
            return value()
        if value is None:
            raise AssertionError(f"unexpected API call {method} {path}")
        return value


class PlanTests(unittest.TestCase):
    def test_current_success_requires_successful_plan_job(self):
        gitlab = FakeGitLab(responses={
            ("GET", "/merge_requests/12/pipelines"): [
                {"id": 7, "sha": "head-sha", "status": "success"},
            ],
            ("GET", "/pipelines/7/jobs"): [{"name": "plan", "status": "success"}],
        })
        self.assertEqual(fixture.ensure_mr_plan(gitlab, 12), "head-sha")
        self.assertFalse(any(method == "POST" for method, _, _ in gitlab.calls))

    def test_historical_success_does_not_satisfy_current_sha(self):
        gitlab = FakeGitLab(responses={
            ("GET", "/merge_requests/12/pipelines"): [
                {"id": 4, "sha": "old-sha", "status": "success"},
            ],
            ("POST", "/merge_requests/12/pipelines"): {"id": 8},
            ("GET", "/pipelines/8/jobs"): [{"name": "plan", "status": "success"}],
            ("GET", "/pipelines/8"): {"sha": "head-sha"},
        })
        with mock.patch.object(fixture, "wait_pipeline"):
            self.assertEqual(fixture.ensure_mr_plan(gitlab, 12), "head-sha")
        self.assertIn(("POST", "/merge_requests/12/pipelines", None), gitlab.calls)

    def test_successful_pipeline_without_successful_plan_job_is_rejected(self):
        gitlab = FakeGitLab(responses={
            ("GET", "/merge_requests/12/pipelines"): [],
            ("POST", "/merge_requests/12/pipelines"): {"id": 8},
            ("GET", "/pipelines/8/jobs"): [{"name": "validate", "status": "success"}],
        })
        with mock.patch.object(fixture, "wait_pipeline"):
            with self.assertRaisesRegex(RuntimeError, "without a successful plan job"):
                fixture.ensure_mr_plan(gitlab, 12)


class CheckpointAndApplyTests(unittest.TestCase):
    def test_sha_change_invalidates_bound_progress(self):
        state = {"deploy_head_sha": "old", "deploy_plan": True, "deploy_apply": True, "deploy_verified": True}
        with mock.patch.object(fixture, "save") as save:
            fixture.bind_checkpoint(state, Path("checkpoint"), "deploy_head_sha", "new",
                                    ("deploy_plan", "deploy_apply", "deploy_verified"))
        self.assertEqual(state, {"deploy_head_sha": "new"})
        save.assert_called_once()

    def test_failed_correct_main_pipeline_is_retried_once(self):
        gitlab = FakeGitLab(
            mr={"iid": 12, "state": "merged", "target_branch": "main", "merge_commit_sha": "merge-sha"},
            responses={
                ("GET", "/pipelines?ref=main&sha=merge-sha"): [
                    {"id": 22, "ref": "main", "sha": "merge-sha", "status": "failed"},
                ],
                ("POST", "/pipelines/22/retry"): {"id": 22},
                ("GET", "/pipelines/22/jobs"): [{"name": "reconcile", "status": "success"}],
            },
        )
        with mock.patch.object(fixture, "wait_pipeline"):
            self.assertEqual(fixture.merge_and_apply(gitlab, 12), (22, "merge-sha"))
        self.assertEqual(sum(call[0:2] == ("POST", "/pipelines/22/retry") for call in gitlab.calls), 1)

    def test_wrong_main_pipeline_sha_is_ignored(self):
        gitlab = FakeGitLab(
            mr={"iid": 12, "state": "merged", "target_branch": "main", "merge_commit_sha": "merge-sha"},
            responses={
                ("GET", "/pipelines?ref=main&sha=merge-sha"): [
                    {"id": 22, "ref": "main", "sha": "other-sha", "status": "failed"},
                ],
            },
        )
        with mock.patch.object(fixture.time, "monotonic", side_effect=[0, 100]), mock.patch.object(fixture.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "did not trigger"):
                fixture.merge_and_apply(gitlab, 12)
        self.assertFalse(any(path.endswith("/retry") for _, path, _ in gitlab.calls))

    def test_successful_main_pipeline_must_run_reconcile(self):
        gitlab = FakeGitLab(
            mr={"iid": 12, "state": "merged", "target_branch": "main", "merge_commit_sha": "merge-sha"},
            responses={
                ("GET", "/pipelines?ref=main&sha=merge-sha"): [
                    {"id": 22, "ref": "main", "sha": "merge-sha", "status": "success"},
                ],
                ("GET", "/pipelines/22/jobs"): [{"name": "validate", "status": "success"}],
            },
        )
        with mock.patch.object(fixture, "wait_pipeline"):
            with self.assertRaisesRegex(RuntimeError, "no successful reconcile job"):
                fixture.merge_and_apply(gitlab, 12)

    def test_mr_target_must_be_main(self):
        gitlab = FakeGitLab(mr={"iid": 12, "state": "opened", "target_branch": "release"})
        with self.assertRaisesRegex(RuntimeError, "expected main"):
            fixture.merge_and_apply(gitlab, 12)
        self.assertEqual(gitlab.calls, [])

    def test_merge_refuses_commit_different_from_planned_sha(self):
        gitlab = FakeGitLab(mr={"iid": 12, "state": "opened", "sha": "new-sha", "target_branch": "main"})
        with self.assertRaisesRegex(RuntimeError, "changed after its successful plan"):
            fixture.merge_and_apply(gitlab, 12, "planned-sha")
        self.assertEqual(gitlab.calls, [])


class InstalledAuthentikEvidenceTests(unittest.TestCase):
    def test_matching_live_evidence_is_reused(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "authentik.json"
            checkpoint = Path(directory) / "checkpoint.json"
            evidence = {
                "test": "installed-authentik-allowed-denied-login", "status": "PASS",
                "source_sha": "merge-sha", "source_fingerprint": "c" * 64, "merge_head_sha": "head-sha",
                "pipeline_id": 18, "pipeline_sha": "merge-sha", "identities_cleaned": True,
                "outcomes": {"allowed_http_status": 200, "allowed_reached_application": True,
                              "denied_http_status": 403, "denied_forbidden": True},
            }
            path.write_text(json.dumps(evidence))
            state = {"deploy_pipeline": 18, "deploy_pipeline_sha": "merge-sha",
                     "authentik_login_head_sha": "head-sha", "authentik_login_pipeline_id": 18,
                     "authentik_login_pipeline_sha": "merge-sha", "authentik_login_source_fingerprint": "c" * 64}
            with mock.patch.object(fixture.subprocess, "run") as run:
                fixture.verify_installed_authentik(state, checkpoint, "head-sha", "c" * 64, path)
            run.assert_not_called()
            self.assertTrue(state["authentik_login_verified"])

    def test_stale_evidence_runs_e2e_again(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "authentik.json"
            checkpoint = Path(directory) / "checkpoint.json"
            path.write_text(json.dumps({"test": "installed-authentik-allowed-denied-login", "status": "PASS"}))
            state = {"deploy_pipeline": 18, "deploy_pipeline_sha": "merge-sha"}
            def run_e2e(command, check):
                self.assertTrue(check)
                self.assertIn("test-gitops-guest-authentik.py", command[1])
                path.write_text(json.dumps({
                    "test": "installed-authentik-allowed-denied-login", "status": "PASS",
                    "source_sha": "merge-sha", "source_fingerprint": "c" * 64, "merge_head_sha": "head-sha",
                    "pipeline_id": 18, "pipeline_sha": "merge-sha", "identities_cleaned": True,
                    "outcomes": {"allowed_http_status": 200, "allowed_reached_application": True,
                                  "denied_http_status": 403, "denied_forbidden": True},
                }))
            with mock.patch.object(fixture.subprocess, "run", side_effect=run_e2e) as run:
                fixture.verify_installed_authentik(state, checkpoint, "head-sha", "c" * 64, path)
            run.assert_called_once()
            self.assertTrue(state["authentik_login_verified"])


class FullResumeEvidenceTests(unittest.TestCase):
    def evidence(self):
        return {
            "schema": 1, "status": "PASS", "source_fingerprint": "c" * 64,
            "deploy_sha": "merge-sha", "interrupted_pipeline_id": 21,
            "interrupted_pipeline_sha": "merge-sha", "retry_pipeline_id": 22,
            "retry_pipeline_sha": "merge-sha", "state_preserved": True,
            "secrets_preserved": True, "states_compared": 6, "cancel_observed": True,
        }

    def test_matching_interruption_and_retry_evidence_is_reused(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence_path = Path(directory) / "resume.json"
            checkpoint = Path(directory) / "checkpoint.json"
            evidence_path.write_text(json.dumps(self.evidence()))
            state = {"deploy_pipeline_sha": "merge-sha", "full_resume_source_fingerprint": "c" * 64}
            with mock.patch.object(fixture.subprocess, "run") as run:
                fixture.verify_full_resume(state, checkpoint, "c" * 64, evidence_path)
            run.assert_not_called()
            self.assertTrue(state["full_resume_verified"])

    def test_resume_evidence_for_another_deploy_runs_drill_again(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence_path = Path(directory) / "resume.json"
            checkpoint = Path(directory) / "checkpoint.json"
            stale = self.evidence()
            stale["deploy_sha"] = "old-sha"
            evidence_path.write_text(json.dumps(stale))
            state = {"deploy_pipeline_sha": "merge-sha"}

            def run_drill(command, check):
                self.assertTrue(check)
                self.assertIn("test-gitops-guest-resume.py", command[1])
                evidence_path.write_text(json.dumps(self.evidence()))

            with mock.patch.object(fixture.subprocess, "run", side_effect=run_drill) as run:
                fixture.verify_full_resume(state, checkpoint, "c" * 64, evidence_path)
            run.assert_called_once()
            self.assertTrue(state["full_resume_verified"])


class RemovalProbeTests(unittest.TestCase):
    def test_dns_nxdomain_proves_absence(self):
        error = socket.gaierror(getattr(socket, "EAI_NONAME", -2), "not found")
        with mock.patch.object(fixture.socket, "gethostbyname", side_effect=error):
            fixture.dns("whoami.lab.test", present=False)

    def test_dns_transient_error_does_not_prove_absence(self):
        error = socket.gaierror(getattr(socket, "EAI_AGAIN", -3), "temporary failure")
        with mock.patch.object(fixture.socket, "gethostbyname", side_effect=error):
            with self.assertRaisesRegex(RuntimeError, "transiently"):
                fixture.dns("whoami.lab.test", present=False)

    def test_grep_errors_do_not_prove_route_absence(self):
        with mock.patch.object(fixture.subprocess, "run", return_value=mock.Mock(returncode=2)):
            with self.assertRaisesRegex(RuntimeError, "grep exited 2"):
                fixture.route_absent()

    def test_grep_no_match_proves_route_absence(self):
        with mock.patch.object(fixture.subprocess, "run", return_value=mock.Mock(returncode=1)):
            fixture.route_absent()


if __name__ == "__main__":
    unittest.main()
