#!/usr/bin/env bash
# Run operator CLI commands with the service identity/environment, without sourcing secrets.
set -euo pipefail
[[ $(uname -s) == Linux && $(id -u) == 0 ]] || { echo 'Run this helper as root on Linux.' >&2; exit 1; }
(($#)) || { echo 'Usage: searchan-admin doctor -strict [-online] | model check | telegram info | telegram webhook -url URL | healthcheck | version' >&2; exit 1; }
case "$1" in doctor|model|telegram|healthcheck|version) ;; *) echo 'Unsupported operator command.' >&2; exit 1;; esac
exec systemd-run --quiet --wait --pipe --collect --unit="searchan-admin-$$" \
  --property=User=searchan --property=Group=searchan \
  --property=WorkingDirectory=/var/lib/searchan \
  --property=EnvironmentFile=/etc/searchan.env \
  --setenv=SEARCHAN_DATA_DIR=/var/lib/searchan \
  --setenv=PATH=/opt/searchan/bin:/usr/local/bin:/usr/bin:/bin \
  /opt/searchan/bin/searchan "$@"
