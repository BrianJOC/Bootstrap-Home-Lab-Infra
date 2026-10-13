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
    def test_missing_coverage_never_becomes_yes(self):
        state = {"source": "abc", "gates": {name: {"status": "PASS"} for name in release.GATES}}
        report = release.verdict(state)
        self.assertFalse(report["ready"])
        self.assertEqual(report["checks"]["raspberry-pi"]["status"], "MISSING_TEST")
        self.assertEqual(report["checks"]["authentik-login"]["status"], "MISSING_TEST")

    def test_not_run_and_failure_fail_closed(self):
        with patch.object(release, "MISSING", {}):
            state = {"source": "abc", "gates": {}}
            self.assertFalse(release.verdict(state)["ready"])
            state["gates"] = {name: {"status": "PASS"} for name in release.GATES}
            self.assertTrue(release.verdict(state)["ready"])
            state["gates"]["guest"] = {"status": "FAIL"}
            self.assertFalse(release.verdict(state)["ready"])

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
