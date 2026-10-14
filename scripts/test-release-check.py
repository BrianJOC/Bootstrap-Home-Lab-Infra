"""Tests of the release verdict, evidence invalidation and execution boundary."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("release_check", Path(__file__).with_name("release-check.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_measured_experimental_gates_and_physical_gate_are_separate(self):
        state = {"source": "abc", "gates": {name: {"status": "PASS"} for name in release.GATES}}
        report = release.verdict(state)
        self.assertTrue(report["ready"])
        self.assertFalse(report["physical_ready"])
        self.assertEqual(report["checks"]["raspberry-pi"]["status"], "MISSING_TEST")
        self.assertEqual(release.verdict({"source": "abc", "gates": {}})["checks"]["authentik-login"]["status"], "NOT_RUN")

    def test_not_run_and_failure_fail_closed(self):
        with patch.object(release, "MISSING", {"raspberry-pi": "physical fixture required"}):
            state = {"source": "abc", "gates": {}}
            self.assertFalse(release.verdict(state)["ready"])
            state["gates"] = {name: {"status": "PASS"} for name in release.GATES}
            self.assertTrue(release.verdict(state)["ready"])
            state["gates"]["guest"] = {"status": "FAIL"}
            self.assertFalse(release.verdict(state)["ready"])

    def test_experimental_readiness_never_implies_physical_readiness(self):
        with patch.object(release, "MISSING", {"raspberry-pi": "physical fixture required"}):
            state = {"source": "abc", "gates": {name: {"status": "PASS"} for name in release.GATES}}
            report = release.verdict(state)
        self.assertTrue(report["ready"])
        self.assertFalse(report["physical_ready"])

    def test_changed_worktree_invalidates_old_passes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "state.json"
            path.write_text(json.dumps({"schema": 1, "source": "old", "gates": {"source": {"status": "PASS"}}}))
            self.assertEqual(release.load_state(path, "old")["gates"]["source"]["status"], "PASS")
            self.assertEqual(release.load_state(path, "new")["gates"], {})

    def test_corrupt_state_does_not_silently_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "state.json"
            path.write_text("not json")
            with self.assertRaises(ValueError):
                release.load_state(path, "new")

    def test_content_and_modes_change_fingerprint(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(release, "ROOT", Path(directory)):
            source = Path(directory) / "main.go"
            source.write_text("old")
            old = release.fingerprint(["main.go"])
            source.write_text("new")
            new = release.fingerprint(["main.go"])
            self.assertNotEqual(old, new)
            source.chmod(0o700)
            self.assertNotEqual(new, release.fingerprint(["main.go"]))

    def test_manifest_excludes_local_secrets_and_caches(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(release, "ROOT", Path(directory)):
            names = ["cmd/main.go", ".env", "config.yaml", "test/keys/test_id_ed25519",
                     "terraform/a.tfstate.backup", "terraform/secret.tfvars", "terraform/env.tfvars.json",
                     "ansible/collections/something.yml", "bin/bootstrap", "bin/hermit", "bin/.go.pkg",
                     "bin/.env", "bin/.secret", ".github/workflows/ci.yml"]
            for name in names:
                path = Path(directory) / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("fixture")
            fake = subprocess.CompletedProcess([], 0, stdout=("\0".join(names) + "\0").encode())
            with patch.object(release.subprocess, "run", return_value=fake):
                self.assertEqual(release.source_paths(), [".github/workflows/ci.yml", "bin/.go.pkg", "bin/hermit", "cmd/main.go"])

    def test_error_summary_does_not_echo_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory) / "test.log"
            log.write_text('password=do-not-display\nThe read operation timed out\n' + json.dumps({"msg": "stage failed", "stage": "k3s", "error": "secret=do-not-display"}))
            summary = release.failure_hint(log)
            self.assertIn("k3s", summary)
            self.assertIn("download timed out", summary)
            self.assertNotIn("do-not-display", summary)

    def test_seed_evidence_requires_current_source_fixture_and_commit(self):
        evidence = {"schema": 1, "status": "PASS", "source_fingerprint": "c" * 64,
                    "main_sha": "a" * 40, "matched_files": 25}
        value = {"evidence": evidence, "manifest_source": "c" * 64, "fixture": "vm:boot"}
        self.assertTrue(release.verify_seed_evidence(value, "c" * 64, "vm:boot")[0])
        self.assertFalse(release.verify_seed_evidence(None, "c" * 64, "vm:boot")[0])
        self.assertFalse(release.verify_seed_evidence({"evidence": "{bad json"}, "c" * 64, "vm:boot")[0])
        self.assertFalse(release.verify_seed_evidence(value, "d" * 64, "vm:boot")[0])
        self.assertFalse(release.verify_seed_evidence(value, "c" * 64, "other:boot")[0])
        failed = json.loads(json.dumps(value))
        failed["evidence"]["status"] = "FAIL"
        self.assertFalse(release.verify_seed_evidence(failed, "c" * 64, "vm:boot")[0])

    def valid_lifecycle_evidence(self, source="c" * 64):
        fixture_id, deploy_sha = "vm:boot", "a" * 40
        checkpoint = {
            "deploy_pipeline": 17, "deploy_pipeline_sha": deploy_sha, "deploy_head_sha": "b" * 40,
            "removal_pipeline": 19, "removal_pipeline_sha": "d" * 40,
            "deploy_verified": True, "no_change_verified": True,
            "removal_verified": True, "removal_rerun_verified": True,
            "authentik_login_verified": True, "authentik_login_source_fingerprint": source,
            "authentik_login_pipeline_id": 17, "authentik_login_pipeline_sha": deploy_sha,
            "authentik_login_head_sha": "b" * 40,
            "full_resume_verified": True, "full_resume_source_fingerprint": source,
            "full_resume_pipeline_sha": deploy_sha,
        }
        outcomes = {"allowed_http_status": 200, "allowed_reached_application": True,
                    "denied_http_status": 403, "denied_forbidden": True}
        auth = {"schema": 1, "test": "installed-authentik-allowed-denied-login", "status": "PASS",
                "source_fingerprint": source, "source_sha": deploy_sha, "merge_head_sha": "b" * 40,
                "pipeline_id": 17, "pipeline_sha": deploy_sha, "identities_cleaned": True,
                "outcomes": outcomes}
        resume = {"schema": 1, "status": "PASS", "source_fingerprint": source,
                  "deploy_sha": deploy_sha, "interrupted_pipeline_id": 21,
                  "interrupted_pipeline_sha": deploy_sha, "interrupted_pipeline_status": "canceled",
                  "retry_pipeline_id": 22,
                  "retry_pipeline_sha": deploy_sha, "state_preserved": True,
                  "secrets_preserved": True, "cancel_observed": True, "states_compared": 6}
        return {"checkpoint": checkpoint, "authentik": auth, "resume": resume, "fixture": fixture_id}

    def test_lifecycle_evidence_requires_checkpoint_bound_current_records(self):
        source, fixture_id = "c" * 64, "vm:boot"
        value = self.valid_lifecycle_evidence(source)
        checks = release.verify_lifecycle_evidence(value, source, fixture_id)
        self.assertTrue(checks["authentik-login"][0])
        self.assertTrue(checks["full-resume"][0])
        self.assertFalse(release.verify_lifecycle_evidence(None, source, fixture_id)["authentik-login"][0])
        corrupt = json.loads(json.dumps(value))
        corrupt["authentik"]["outcomes"] = "{bad json"
        self.assertFalse(release.verify_lifecycle_evidence(corrupt, source, fixture_id)["authentik-login"][0])
        stale = json.loads(json.dumps(value))
        stale["resume"]["source_fingerprint"] = "e" * 64
        self.assertFalse(release.verify_lifecycle_evidence(stale, source, fixture_id)["full-resume"][0])
        for field, invalid in (("pipeline_id", 999), ("pipeline_sha", "e" * 40)):
            with self.subTest(field=field):
                wrong_pipeline = json.loads(json.dumps(value))
                wrong_pipeline["authentik"][field] = invalid
                self.assertFalse(release.verify_lifecycle_evidence(wrong_pipeline, source, fixture_id)["authentik-login"][0])
        wrong_fixture = json.loads(json.dumps(value))
        wrong_fixture["fixture"] = "other:boot"
        self.assertFalse(release.verify_lifecycle_evidence(wrong_fixture, source, fixture_id)["authentik-login"][0])

    def test_verify_invalidates_old_passes_before_probe_or_sync_failure(self):
        source = "c" * 64

        class FakeRunner:
            def __init__(self):
                self.state = {"source": source, "fixture": "vm:boot",
                              "acceptance_checkpoint": "logs/acceptance-old.json",
                              "gates": {name: {"status": "PASS"} for name in release.GATES}}

            def record(self, name, status, detail, log=None):
                self.state["gates"][name] = {"status": status, "detail": detail}

        for probe_result, sync_result in ((False, True), (True, False)):
            with self.subTest(probe=probe_result, sync=sync_result):
                runner = FakeRunner()
                with (patch.object(release, "probe", return_value=probe_result),
                      patch.object(release, "sync_source", return_value=sync_result),
                      patch.object(release, "write_json"),
                      patch.object(release, "OUT", Path("/tmp/release-check-test"))):
                    self.assertFalse(release.verify(runner))
                self.assertFalse(release.verdict(runner.state)["ready"])
                self.assertNotEqual(runner.state["acceptance_checkpoint"], "logs/acceptance-old.json")

    def test_verify_preserves_unfinished_checkpoint_on_early_failure(self):
        class FakeRunner:
            def __init__(self):
                self.state = {"source": "c" * 64, "fixture": "vm:boot",
                              "acceptance_checkpoint": "logs/acceptance-resume.json",
                              "gates": {name: {"status": "NOT_RUN"} for name in release.GATES}}
                self.state["gates"]["merge-lifecycle"] = {"status": "FAIL"}

            def record(self, name, status, detail, log=None):
                self.state["gates"][name] = {"status": status, "detail": detail}

        runner = FakeRunner()
        with (patch.object(release, "probe", return_value=False),
              patch.object(release, "write_json"),
              patch.object(release, "OUT", Path("/tmp/release-check-test"))):
            self.assertFalse(release.verify(runner))
        self.assertEqual(runner.state["acceptance_checkpoint"], "logs/acceptance-resume.json")

    def test_verify_passes_current_fingerprint_to_merge_script(self):
        source = "c" * 64

        class FakeRunner:
            def __init__(self):
                self.state = {"source": source, "fixture": "vm:boot", "gates": {}}
                self.commands = []

            def guest(self, name, command, timeout):
                self.commands.append((name, command))
                return name != "merge-lifecycle"

            def record(self, name, status, detail, log=None):
                self.state["gates"][name] = {"status": status, "detail": detail}

        seed = {"evidence": {"schema": 1, "status": "PASS", "source_fingerprint": source,
                             "main_sha": "a" * 40, "matched_files": 25},
                "manifest_source": source, "fixture": "vm:boot"}
        runner = FakeRunner()
        with (patch.object(release, "probe", return_value=True),
              patch.object(release, "sync_source", return_value=True),
              patch.object(release, "guest_json", return_value=seed),
              patch.object(release, "write_json"),
              patch.object(release, "OUT", Path("/tmp/release-check-test"))):
            self.assertFalse(release.verify(runner))
        merge = next(command for name, command in runner.commands if name == "merge-lifecycle")
        self.assertIn("--source-fingerprint " + source, merge)
        self.assertEqual(runner.state["gates"]["authentik-login"]["status"], "FAIL")
        self.assertEqual(runner.state["gates"]["full-resume"]["status"], "FAIL")

    def test_successful_merge_without_bound_evidence_cannot_leave_ready_yes(self):
        source = "c" * 64

        class FakeRunner:
            def __init__(self):
                self.state = {"source": source, "fixture": "vm:boot",
                              "acceptance_checkpoint": "logs/acceptance-old.json",
                              "gates": {name: {"status": "PASS"} for name in release.GATES}}

            def guest(self, name, command, timeout):
                self.state["gates"][name] = {"status": "PASS"}
                return True

            def record(self, name, status, detail, log=None):
                self.state["gates"][name] = {"status": status, "detail": detail}

        seed = {"evidence": {"schema": 1, "status": "PASS", "source_fingerprint": source,
                             "main_sha": "a" * 40, "matched_files": 25},
                "manifest_source": source, "fixture": "vm:boot"}
        for lifecycle in (None, self.valid_lifecycle_evidence()):
            with self.subTest(lifecycle="missing" if lifecycle is None else "wrong-source"):
                runner = FakeRunner()
                if lifecycle is not None:
                    lifecycle["authentik"]["source_fingerprint"] = "d" * 64
                with (patch.object(release, "probe", return_value=True),
                      patch.object(release, "sync_source", return_value=True),
                      patch.object(release, "guest_json", side_effect=(seed, lifecycle)),
                      patch.object(release, "write_json"),
                      patch.object(release, "OUT", Path("/tmp/release-check-test"))):
                    self.assertFalse(release.verify(runner))
                self.assertEqual(runner.state["gates"]["merge-lifecycle"]["status"], "PASS")
                self.assertFalse(release.verdict(runner.state)["ready"])
                self.assertEqual(runner.state["gates"]["authentik-login"]["status"], "FAIL")

    def test_unsafe_source_symlink_rejected(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(release, "ROOT", Path(directory)):
            root = Path(directory)
            (root / "scripts").mkdir()
            (root / "scripts/escape.py").symlink_to(__file__)
            fake = subprocess.CompletedProcess([], 0, stdout=b"scripts/escape.py\0")
            with patch.object(release.subprocess, "run", return_value=fake), self.assertRaises(RuntimeError):
                release.source_paths()

    def test_execution_timeout_returns_failure_and_private_log(self):
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory) / "test.log"
            code = release.execute([sys.executable, "-c", "import time; time.sleep(10)"], log, 0.05)
            self.assertEqual(code, 124)
            self.assertEqual(log.stat().st_mode & 0o777, 0o600)

    def test_standup_never_destroys_snapshot(self):
        class Fake:
            def __init__(self):
                self.tasks = []
                self.state = {"gates": {}}

            def task(self, name, recipe, timeout):
                self.tasks.append(recipe)
                return True

            def guest(self, *args):
                return False  # Identity failure stops before transfer/mutation.

        with tempfile.TemporaryDirectory() as directory, patch.object(release, "ROOT", Path(directory)), patch.object(release.tempfile, "gettempdir", return_value=directory):
            snapshot = Path(directory) / "test/images/run-snapshot.qcow2"
            snapshot.parent.mkdir(parents=True)
            snapshot.write_bytes(b"preserve")
            fake = Fake()
            self.assertFalse(release.standup(fake, []))
            self.assertIn("vm-resume", fake.tasks)
            self.assertNotIn("vm-up", fake.tasks)
            self.assertNotIn("vm-down", fake.tasks)
            self.assertEqual(snapshot.read_bytes(), b"preserve")


if __name__ == "__main__":
    unittest.main()
