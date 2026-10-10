#!/usr/bin/env bash
set -euo pipefail
[[ ${1:-} == tag ]] || { echo 'Release requires an existing version tag, not a branch.' >&2; exit 1; }
task_tag=${2:-}
task_number='(0|[1-9][0-9]*)'
task_pattern="^v$task_number\.$task_number\.$task_number(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$"
[[ "$task_tag" =~ $task_pattern && ${#task_tag} -le 64 ]] || { echo 'Use vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N (max 64 chars).' >&2; exit 1; }
task_revision=$(git rev-parse --verify HEAD)
[[ $(git rev-parse --verify "refs/tags/$task_tag^{commit}") == "$task_revision" ]] || { echo 'Version tag does not match checked-out commit.' >&2; exit 1; }
task_prerelease=false
[[ "$task_tag" != *-* ]] || task_prerelease=true
printf 'tag=%s\nversion=%s\nrevision=%s\nprerelease=%s\n' "$task_tag" "${task_tag#v}" "$task_revision" "$task_prerelease"
