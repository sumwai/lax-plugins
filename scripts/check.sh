#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
package_dir=""
binary="$work/bin/lax"

cleanup() {
  if [[ -n "$package_dir" ]]; then
    rm -rf "$package_dir"
  fi
  rm -rf "$work"
}
trap cleanup EXIT

unformatted="$(gofmt -l "$repo_root/plugins/doubao-seed-tts/plugin_test.go")"
if [[ -n "$unformatted" ]]; then
  printf 'gofmt required:\n%s\n' "$unformatted" >&2
  exit 1
fi

if [[ -n "${LAX_DIR:-}" ]]; then
  lax_dir="$(cd "$LAX_DIR" && pwd)"
else
  lax_dir="$work/lax"
  git clone --depth 1 "${LAX_REPO:-https://github.com/sumwai/lax.git}" "$lax_dir"
fi

# The plugin tests import lax/internal/plugin, so they must run inside a lax
# checkout. Copy into a uniquely named ignored directory and remove it after.
mkdir -p "$lax_dir/plugins"
package_dir="$(mktemp -d "$lax_dir/plugins/lax-plugins-check.XXXXXX")"
cp -a "$repo_root/plugins/doubao-seed-tts/." "$package_dir/"
package_name="$(basename "$package_dir")"

(
  cd "$lax_dir"
  go test "./plugins/$package_name"
  mkdir -p "$(dirname "$binary")"
  go build -o "$binary" ./cmd/lax
)

mkdir -p "$work/config" "$work/data"
export XDG_CONFIG_HOME="$work/config"
export XDG_DATA_HOME="$work/data"

"$binary" init

# Older lax releases only accept local directories. Once the checked-out lax
# understands GitHub sources, exercise the documented install path too.
if "$binary" plugin install --help 2>&1 | grep -q 'GitHub'; then
  "$binary" plugin install \
    "https://github.com/sumwai/lax-plugins/tree/master/plugins/doubao-seed-tts" \
    --trust sandboxed --dry-run
fi

# Pin to a released tag. This fails if the tag is missing, which is the point:
# users who write name@version depend on these tags staying available.
if "$binary" plugin install --help 2>&1 | grep -q '官方插件名'; then
  "$binary" plugin install doubao-seed-tts@1.2.1 --trust sandboxed --dry-run
fi

"$binary" plugin install "$repo_root/plugins/doubao-seed-tts" --trust sandboxed --yes
"$binary" plugin doctor
"$binary" plugin verify --all
