#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

output_directory="$(mktemp -d "${TMPDIR:-/tmp}/mcp-file-tools-rootfs.XXXXXX")"
trap 'rm -rf "$output_directory"' EXIT

targets=(
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
  darwin/amd64
  darwin/arm64
)

for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  extension=""
  if [[ "$goos" == "windows" ]]; then
    extension=".exe"
  fi
  output="$output_directory/rootfs-${goos}-${goarch}.test${extension}"
  printf 'compile-only rootfs tests: %s/%s\n' "$goos" "$goarch"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go test -c -o "$output" ./internal/rootfs
done

printf 'compile-only checks passed; native Windows and macOS evidence remains required by Stage 19\n'
