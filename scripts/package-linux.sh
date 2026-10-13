#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
task_version=${1:?Usage: package-linux.sh VERSION [OUTPUT_DIRECTORY]}
[[ "$task_version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { echo 'Invalid version' >&2; exit 1; }
task_output=${2:-dist}
task_go=${GO_BIN:-go}
task_revision=${REVISION:-$(git rev-parse HEAD 2>/dev/null || printf unknown)}
[[ "$task_revision" == unknown || "$task_revision" =~ ^[a-fA-F0-9]{7,64}$ ]] || { echo 'Invalid revision' >&2; exit 1; }
task_go_version=$("$task_go" env GOVERSION)
mkdir -p "$task_output"
task_output=$(cd "$task_output" && pwd)
task_work=$(mktemp -d)
trap 'rm -rf "$task_work"' EXIT
for task_arch in amd64 arm64; do
  task_name="searchan-$task_version-linux-$task_arch"
  task_bundle="$task_work/$task_name"
  mkdir -p "$task_bundle/bin" "$task_bundle/scripts" "$task_bundle/deploy" "$task_bundle/quality"
  CGO_ENABLED=0 GOOS=linux GOARCH="$task_arch" "$task_go" build -trimpath \
    -ldflags="-s -w -X main.version=$task_version -X main.commit=$task_revision" \
    -o "$task_bundle/bin/searchan" ./cmd/searchan
  cp scripts/install-linux.sh scripts/admin-linux.sh "$task_bundle/scripts/"
  cp deploy/searchan.service deploy/Caddyfile.native deploy/DOCKER.md "$task_bundle/deploy/"
  cp .env.example DEPLOYMENT.md "$task_bundle/"
  cp README.md CHANGELOG.md PLAN.md RELEASE_READINESS.md QUALITY_EVALUATION.md QUALITY_BASELINE.md QUALITY_ITERATION_*.md SEARCH_REVIEW.md "$task_bundle/"
  cp quality/cases.json "$task_bundle/quality/"
  printf '%s\n' "$task_version" > "$task_bundle/RELEASE_VERSION"
  printf '{"version":"%s","commit":"%s","go":"%s","os":"linux","arch":"%s"}\n' \
    "$task_version" "$task_revision" "$task_go_version" "$task_arch" > "$task_bundle/BUILD_INFO.json"
  # macOS tar otherwise adds AppleDouble sidecars to Linux bundles.
  COPYFILE_DISABLE=1 tar -czf "$task_output/$task_name.tar.gz" -C "$task_work" "$task_name"
done
(
  cd "$task_output"
  if command -v sha256sum >/dev/null; then
    sha256sum "searchan-$task_version-linux-amd64.tar.gz" "searchan-$task_version-linux-arm64.tar.gz"
  else
    shasum -a 256 "searchan-$task_version-linux-amd64.tar.gz" "searchan-$task_version-linux-arm64.tar.gz"
  fi
) > "$task_output/SHA256SUMS-$task_version"
printf 'Linux amd64/arm64 bundles and checksums: %s\n' "$task_output"
