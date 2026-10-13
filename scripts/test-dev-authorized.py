"""Check the development approval boundary without mutating a VM."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("dev-authorized.py")
spec = importlib.util.spec_from_file_location("dev_authorized", SCRIPT)
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)


class BoundaryTests(unittest.TestCase):
    def test_source_scope(self):
        self.assertEqual(dev.source_file("internal/gitops/project.go")[1], "internal/gitops/project.go")
        for name in ("/etc/passwd", "../outside.py", ".env", "test/keys/test_id_ed25519",
                     "terraform/.terraform/credentials.yaml", "terraform/secrets.tfvars",
                     "ansible/collections/file.yaml", "internal/../../outside.py"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                dev.source_file(name)

    def test_symlink_rejected(self):
        with tempfile.TemporaryDirectory(dir=dev.ROOT / "scripts") as directory:
            link = Path(directory) / "link.py"
            link.symlink_to(SCRIPT)
            with self.assertRaises(ValueError):
                dev.source_file(str(link.relative_to(dev.ROOT)))

    def test_guest_scope(self):
        command = dev.ssh_command("printf ok")
        self.assertEqual(command[command.index("-p") + 1], "2222")
        self.assertEqual(command[-2], "ansible@127.0.0.1")
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn("ForwardAgent=no", command)
        self.assertIn("bootstrap-test", command[-1])

    def test_no_arbitrary_tasks_or_target_overrides(self):
        for args in (("task", "run"), ("task", "doctor", "run"),
                     ("task", "backup-now"), ("guest", "true", "--host", "real.example")):
            result = subprocess.run([sys.executable, str(SCRIPT), *args], capture_output=True)
            self.assertEqual(result.returncode, 2)


if __name__ == "__main__":
    unittest.main()
