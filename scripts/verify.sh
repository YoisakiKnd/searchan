#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_go_bin=${GO_BIN:-go}
task_typst_bin=${TYPST_BIN:-typst}
task_vuln_bin=${GOVULNCHECK_BIN:-govulncheck}
command -v "$task_go_bin" >/dev/null
command -v "$task_typst_bin" >/dev/null
command -v "$task_vuln_bin" >/dev/null
# govulncheck invokes "go" internally, so honor an explicit GO_BIN there too.
task_go_path=$(command -v "$task_go_bin")
PATH="$(dirname "$task_go_path"):$PATH"
export PATH
export SEARCHAN_TEST_TYPST="$task_typst_bin"
bash -n scripts/install-linux.sh scripts/admin-linux.sh scripts/package-linux.sh
"$task_go_bin" test -race -cover -timeout 90s ./...
"$task_go_bin" vet ./...
"$task_vuln_bin" ./...
"$task_go_bin" build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev} -X main.commit=${REVISION:-unknown}" -o bin/searchan ./cmd/searchan
./bin/searchan eval -out artifacts/evaluation.json
./bin/searchan demo -typst "$task_typst_bin" -out artifacts/verification/render
