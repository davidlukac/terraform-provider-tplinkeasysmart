#!/bin/sh
# Runs `terraform validate` on every documentation example against the locally
# built provider (dev_overrides). Nothing talks to a switch: validate only
# checks the configuration against the schemas.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
go build -o bin/terraform-provider-tplinkeasysmart .
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
CLI="$WORK/terraformrc"
cat > "$CLI" <<CLIEOF
provider_installation {
  dev_overrides { "local-infra/tplinkeasysmart" = "$ROOT/bin" }
  direct {}
}
CLIEOF
fail=0
for dir in examples/provider examples/resources/* examples/data-sources/*; do
  tf=$(ls "$dir"/*.tf 2>/dev/null | head -1) || continue
  [ -n "$tf" ] || continue
  d="$WORK/$(echo "$dir" | tr / _)"
  mkdir -p "$d"
  cp "$dir"/*.tf "$d"/
  : > "$d/zz_provider.tf"
  if ! grep -q 'required_providers' "$d"/*.tf; then
    cat >> "$d/zz_provider.tf" <<'TFEOF'
terraform {
  required_providers {
    tplinkeasysmart = { source = "local-infra/tplinkeasysmart" }
  }
}
TFEOF
  fi
  if grep -q 'provider "tplinkeasysmart"' "$d"/*.tf; then :; else
    cat >> "$d/zz_provider.tf" <<'TFEOF'
provider "tplinkeasysmart" {
  host     = "192.0.2.1"
  username = "admin"
  password = "not-a-real-password"
}
TFEOF
  fi
  if out=$(cd "$d" && TF_CLI_CONFIG_FILE="$CLI" terraform validate -no-color 2>&1); then
    echo "ok    $dir"
  else
    echo "FAIL  $dir"; echo "$out" | grep -vE 'Warning: Provider development overrides|^\s*$|dev_overrides|behavior may|published releases|applying changes|following provider|local-infra/tplinkeasysmart in|^[│╵╷]' | head -8
    fail=1
  fi
done
exit $fail
