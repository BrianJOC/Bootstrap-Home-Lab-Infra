#!/usr/bin/env bash
set -Eeuo pipefail

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-home-infra-generated-iac.XXXXXX")"
cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/bootstrap-home-infra-generated-iac.*) rm -rf -- "$work_dir" ;;
    *) echo "refusing to remove unexpected generated-IaC path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT

terraform_root="$work_dir/project/terraform"
mkdir -p "$terraform_root" "$work_dir/project/infrastructure/nodes"
cp terraform/modules/gitlab-config/templates/versions.tf "$terraform_root/versions.tf"
cp terraform/modules/gitlab-config/templates/maas-machines.tf "$terraform_root/maas-machines.tf"

plugin_cache="${TMPDIR:-/tmp}/bootstrap-home-infra-terraform-plugin-cache"
mkdir -p "$plugin_cache"
TF_PLUGIN_CACHE_DIR="$plugin_cache" terraform -chdir="$terraform_root" init -backend=false -input=false
TF_PLUGIN_CACHE_DIR="$plugin_cache" terraform -chdir="$terraform_root" validate
