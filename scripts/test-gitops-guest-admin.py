"""Verify an initialized disposable GitLab preserves its admin on bootstrap rerun."""

import json
from pathlib import Path
import socket
import subprocess

import yaml

if socket.gethostname() != "bootstrap-test" or Path.home() != Path("/home/ansible"):
    raise SystemExit("this fixture only runs as ansible inside the disposable QEMU guest")

tasks = yaml.safe_load(Path("ansible/roles/gitlab_install/tasks/configure.yml").read_text())
task = next(t for t in tasks if t.get("name") == "Complete interrupted GitLab administrator initialization")
body = task["ansible.builtin.command"]["argv"][-1]
# Execute the actual task body against the real guest database. A different
# synthetic seed password makes accidental credential resets observable.
probe = """
user = User.find_by!(username: 'root')
abort('Root must already be an administrator') unless user.admin?
before_password = user.encrypted_password
before_users = User.count
ENV['GITLAB_ROOT_PASSWORD'] = 'RerunFixture-3b9P6s2L8m4R7k1N'
""" + "eval(" + json.dumps(body) + ")\n" + """
abort('Existing administrator credential changed') unless user.reload.encrypted_password == before_password
abort('Rerun changed the user count') unless User.count == before_users
puts 'Existing administrator and credential preserved'
"""
result = subprocess.run(["sudo", "gitlab-rails", "runner", "-"], input=probe,
                        text=True, capture_output=True, timeout=120)
if result.returncode != 0:
    raise SystemExit("administrator preservation probe failed; sensitive output withheld")
if "BOOTSTRAP_ADMIN_SEEDED" in result.stdout:
    raise SystemExit("initialized GitLab unexpectedly ran the seed task")
print("PASS: actual bootstrap task preserves the existing administrator and password")
