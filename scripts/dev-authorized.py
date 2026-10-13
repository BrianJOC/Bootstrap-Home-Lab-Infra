"""Bounded local development operations for a persistent Codex approval rule.

Run with this repository's bin/python. This is trusted repository code, not a
sandbox against malicious changes to the source or commands inside the guest.
"""

import argparse
import os
from pathlib import Path, PurePosixPath
import shlex
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
GUEST_ROOT = "/home/ansible/bootstrap-gitops-test"
TASKS = (
    "doctor", "doctor-integration", "dev-setup", "build", "conflict-check",
    "fmt-check", "test-go", "syntax-check-all", "lint-all", "tf-validate",
    "test-gitops-generated", "test-gitops-access", "test-gitops-handoff",
    "vm-up", "vm-resume", "vm-down", "mocks-up", "mocks-down",
    "release-status", "release-source", "release-standup", "release-verify", "test-release-check",
)


def ssh_command(command):
    # Ignore user SSH configuration and disable forwarding; use only the test key.
    # Keep host verification enabled. No host/port/key override is accepted.
    guard = 'test "$(hostname)" = bootstrap-test && test "$(id -un)" = ansible'
    return [
        "/usr/bin/ssh", "-F", "/dev/null", "-p", "2222", "-i",
        str(ROOT / "test/keys/test_id_ed25519"),
        "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=yes", "-o", "ClearAllForwardings=yes",
        "-o", "ForwardAgent=no", "-o", "ConnectTimeout=10",
        "ansible@127.0.0.1", guard + " && " + command,
    ]


def source_file(name):
    path = PurePosixPath(name)
    allowed_roots = {"ansible", "charts", "cmd", "internal", "terraform", "scripts", "gitops"}
    allowed_files = {"go.mod", "go.sum", "Justfile", "requirements-dev.lock"}
    if (path.is_absolute() or ".." in path.parts or not path.parts
            or any(part.startswith(".") for part in path.parts)
            or (path.parts[0] not in allowed_roots and name not in allowed_files)
            or path.suffix not in {".go", ".mod", ".sum", ".yaml", ".yml", ".tf", ".j2", ".sh", ".py", ".hcl", ".lock", ".cfg", ""}
            or "collections" in path.parts):
        raise ValueError("Only repository source files may be transferred")
    local = ROOT / path
    for item in (local, *local.parents):
        if item == ROOT:
            break
        if item.is_symlink():
            raise ValueError("Symlinks are not transferable")
    if not local.is_file() or not local.resolve().is_relative_to(ROOT):
        raise ValueError("Source must be a regular file inside this repository")
    return local, str(path)


def run(command, log=False, stdin=None):
    env = os.environ.copy()
    env["PATH"] = f"{ROOT / '.venv/bin'}:{ROOT / 'bin'}:{env.get('PATH', '')}"
    if log:
        with tempfile.NamedTemporaryFile(prefix="bootstrap-dev-", suffix=".log", dir="/tmp", delete=False) as output:
            print(f"Log: {output.name}", flush=True)
            result = subprocess.run(command, cwd=ROOT, env=env, stdin=stdin,
                                    stdout=output, stderr=subprocess.STDOUT, check=False)
        print(f"Exit: {result.returncode}", flush=True)
        return result.returncode
    return subprocess.run(command, cwd=ROOT, env=env, stdin=stdin, check=False).returncode


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="operation", required=True)
    task = commands.add_parser("task")
    task.add_argument("name", choices=TASKS)
    task.add_argument("--log", action="store_true")
    guest = commands.add_parser("guest")
    guest.add_argument("command", help="Command executed only inside the disposable guest")
    guest.add_argument("--log", action="store_true")
    put = commands.add_parser("put")
    put.add_argument("source", help="Repository-relative source file; destination is fixed")
    args = parser.parse_args()
    if args.operation == "task":
        return run([str(ROOT / "bin/just"), "--justfile", str(ROOT / "Justfile"),
                    "--working-directory", str(ROOT), args.name], args.log)
    if args.operation == "guest":
        return run(ssh_command("sh -c " + shlex.quote(args.command)), args.log)
    try:
        local, relative = source_file(args.source)
    except ValueError as error:
        parser.error(str(error))
    destination = GUEST_ROOT + "/" + relative
    # Fixed guest checkout; command arguments are quoted, data travels on stdin.
    command = "mkdir -p -- " + shlex.quote(str(PurePosixPath(destination).parent))
    command += " && cat > " + shlex.quote(destination)
    with local.open("rb") as content:
        return run(ssh_command(command), stdin=content)


if __name__ == "__main__":
    raise SystemExit(main())
