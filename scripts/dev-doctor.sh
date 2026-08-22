#!/usr/bin/env bash
set -Eeuo pipefail

if [ -d .venv/bin ]; then
  PATH="$PWD/.venv/bin:$PATH"
fi

mode="${1:-source}"
case "$mode" in
  source|integration) ;;
  *)
    echo "usage: $0 [source|integration]" >&2
    exit 2
    ;;
esac

failures=0
check_command() {
  local command_name="$1"
  if command -v "$command_name" >/dev/null 2>&1; then
    printf 'ok       %-24s %s\n' "$command_name" "$(command -v "$command_name")"
  else
    printf 'missing  %-24s\n' "$command_name"
    failures=$((failures + 1))
  fi
}

for command_name in go terraform ansible-playbook ansible-lint ansible-galaxy rg git; do
  check_command "$command_name"
done

for hermit_tool in bin/hermit bin/python3 bin/uv bin/just; do
  if [ -x "$hermit_tool" ]; then
    printf 'ok       %-24s %s\n' "$hermit_tool" "$("$hermit_tool" --version 2>/dev/null | head -1)"
  else
    printf 'missing  %-24s restore the Hermit environment\n' "$hermit_tool"
    failures=$((failures + 1))
  fi
done

if [ "$mode" = integration ]; then
  if [ -x .venv/bin/molecule ]; then
    printf 'ok       %-24s %s\n' molecule .venv/bin/molecule
  else
    printf 'missing  %-24s run: just dev-setup\n' molecule
    failures=$((failures + 1))
  fi

  for command_name in docker qemu-system-aarch64 qemu-img packer ssh-keyscan; do
    check_command "$command_name"
  done

  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    printf 'ok       %-24s running\n' docker-daemon
  else
    printf 'blocked  %-24s start Docker Desktop or another local Docker daemon\n' docker-daemon
    failures=$((failures + 1))
  fi

  if [ -f test/images/output/base-aarch64.qcow2 ]; then
    printf 'ok       %-24s %s\n' qemu-base test/images/output/base-aarch64.qcow2
  else
    printf 'missing  %-24s run: just build-base-image\n' qemu-base
    failures=$((failures + 1))
  fi

  if [ -f test/keys/test_id_ed25519 ]; then
    printf 'ok       %-24s present\n' test-ssh-key
  else
    printf 'missing  %-24s run: just generate-test-keys\n' test-ssh-key
    failures=$((failures + 1))
  fi
fi

if [ "$failures" -ne 0 ]; then
  printf '\n%d prerequisite(s) need attention for %s testing.\n' "$failures" "$mode" >&2
  exit 1
fi

printf '\nAll %s prerequisites are available.\n' "$mode"
