#!/usr/bin/env bash
# Installs trusted local artifacts. Never evaluates an environment file as shell.
set -Eeuo pipefail
umask 077
task_script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
task_source_root=$(cd "$task_script_dir/.." && pwd)
task_dest=/ task_binary= task_typst= task_archive= task_sha= task_env= task_version=
task_build=false task_deps=false task_replace=false task_start=auto task_rollback=
fail() { printf 'searchan installer: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
Usage: install-linux.sh [options]
  --binary PATH            Trusted Linux searchan executable (bundle default: bin/searchan)
  --from-source            Build this checkout using installed Go >=1.26
  --typst PATH             Trusted installed Typst executable (default: PATH)
  --typst-archive PATH     Local official Linux Typst .tar.xz archive
  --typst-sha256 HASH      Required SHA-256 for --typst-archive
  --env-file PATH          Initial systemd environment file, never printed or sourced
  --replace-env            Explicitly replace existing config; restored on start failure
  --version NAME           Release name (bundle default: RELEASE_VERSION)
  --install-deps           Install Debian/Ubuntu CA certificates and Noto CJK fonts via apt
  --start / --no-start     Start now / leave stopped (default: preserve previous running state)
  --rollback NAME          Activate an already installed release
  --destdir PATH           Stage files only; no users, packages, systemd, or live starts
EOF
}
while (($#)); do
  case "$1" in
    --binary|--typst|--typst-archive|--typst-sha256|--env-file|--version|--rollback|--destdir)
      (($# >= 2)) || fail "missing value for $1"
      case "$1" in
        --binary) task_binary=$2;; --typst) task_typst=$2;;
        --typst-archive) task_archive=$2;; --typst-sha256) task_sha=$2;;
        --env-file) task_env=$2;; --version) task_version=$2;;
        --rollback) task_rollback=$2;; --destdir) task_dest=$2;;
      esac; shift 2;;
    --from-source) task_build=true; shift;;
    --install-deps) task_deps=true; shift;;
    --replace-env) task_replace=true; shift;;
    --start) task_start=yes; shift;; --no-start) task_start=no; shift;;
    -h|--help) usage; exit 0;; *) fail "unknown option: $1";;
  esac
done
[[ -d "$task_dest" ]] || fail 'destination directory must already exist'
task_dest=$(cd "$task_dest" && pwd -P)
task_stage=false
if [[ "$task_dest" != / ]]; then task_stage=true; else
  [[ $(uname -s) == Linux ]] || fail 'live installation supports Linux only'
  [[ $(id -u) == 0 ]] || fail 'live installation requires root'
  command -v systemctl >/dev/null || fail 'systemd is required'
  [[ -d /run/systemd/system ]] || fail 'systemd is not running'
fi
[[ -z "$task_env" || -f "$task_env" ]] || fail 'environment file does not exist'
[[ -f "$task_source_root/deploy/searchan.service" && -f "$task_source_root/.env.example" && -f "$task_source_root/scripts/admin-linux.sh" ]] || fail 'keep installer with deploy/searchan.service, scripts/admin-linux.sh and .env.example'
task_prefix="$task_dest/opt/searchan"
task_config="$task_dest/etc/searchan.env"
task_unit="$task_dest/etc/systemd/system/searchan.service"
if [[ -n "$task_env" && -e "$task_config" && "$task_replace" != true ]]; then fail 'config exists; use --replace-env only when replacement is intended'; fi
[[ "$task_build" != true || -z "$task_binary" ]] || fail 'choose --binary or --from-source'
[[ -z "$task_archive" || -z "$task_typst" ]] || fail 'choose --typst or --typst-archive'
task_work=$(mktemp -d)
task_activated=false task_finished=false task_was_active=false task_old_link= task_had_config=false task_had_unit=false task_release_stage= task_stage_lock=
atomic_link() {
  local target=$1 link=$2
  ln -s "$target" "$link.next-$$"
  if [[ $(uname -s) == Darwin && "$task_stage" == true ]]; then
    mv -fh "$link.next-$$" "$link" || { rm -f "$link.next-$$"; return 1; }
  else
    mv -fT "$link.next-$$" "$link" || { rm -f "$link.next-$$"; return 1; }
  fi
}
cleanup() {
  local status=$?
  if [[ "$task_activated" == true && "$task_finished" != true ]]; then
    printf 'Installation/start failed; restoring previous activation.\n' >&2
    if [[ -n "$task_old_link" ]]; then atomic_link "$task_old_link" "$task_prefix/current" || true; else rm -f "$task_prefix/current"; fi
    if [[ "$task_had_config" == true ]]; then install -m 0640 "$task_work/config.old" "$task_config"; else rm -f "$task_config"; fi
    if [[ "$task_had_unit" == true ]]; then install -m 0644 "$task_work/service.old" "$task_unit"; else rm -f "$task_unit"; fi
    if [[ "$task_stage" != true ]]; then
      chown root:searchan "$task_config" 2>/dev/null || true
      systemctl daemon-reload || true
      if [[ "$task_was_active" == true ]]; then systemctl start searchan || true; fi
    fi
  fi
  [[ -z "$task_release_stage" ]] || rm -rf "$task_release_stage"
  [[ -z "$task_stage_lock" ]] || rmdir "$task_stage_lock"
  rm -rf "$task_work"
  return "$status"
}
trap cleanup EXIT
if [[ -n "$task_env" ]]; then cp "$task_env" "$task_work/config.new"; task_env="$task_work/config.new"; fi
install -d -m 0755 "$task_prefix"
if [[ "$task_stage" == true ]]; then
  mkdir "$task_prefix/.install-lock" || fail 'another staged installation is in progress'
  task_stage_lock="$task_prefix/.install-lock"
else
  command -v flock >/dev/null || fail 'flock (util-linux) is required'
  exec 9>"$task_prefix/.install.lock"
  flock -n 9 || fail 'another installation is in progress'
fi
if [[ -n "$task_env" && -e "$task_config" && "$task_replace" != true ]]; then fail 'config exists; use --replace-env only when replacement is intended'; fi
if [[ -n "$task_rollback" ]]; then task_version=$task_rollback; fi
if [[ -z "$task_version" && -f "$task_source_root/RELEASE_VERSION" ]]; then read -r task_version < "$task_source_root/RELEASE_VERSION"; fi
task_version=${task_version:-local-$(date -u +%Y%m%dT%H%M%SZ)}
[[ "$task_version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || fail 'invalid release name'
task_release="$task_prefix/releases/$task_version"
if [[ -z "$task_rollback" ]]; then
  if [[ "$task_build" == true ]]; then
    task_go=${GO_BIN:-go}
    (cd "$task_source_root" && GOTOOLCHAIN=local CGO_ENABLED=0 "$task_go" build -trimpath -ldflags="-s -w -X main.version=$task_version" -o "$task_work/searchan" ./cmd/searchan)
    task_binary="$task_work/searchan"
  fi
  task_binary=${task_binary:-$task_source_root/bin/searchan}
  [[ -f "$task_binary" && -x "$task_binary" ]] || fail 'provide a Linux binary or use --from-source with Go >=1.26'
  if [[ -n "$task_archive" ]]; then
    [[ "$task_sha" =~ ^[a-fA-F0-9]{64}$ ]] || fail '--typst-archive requires an expected SHA-256'
    [[ -f "$task_archive" ]] || fail 'Typst archive missing'
    task_actual=$(sha256sum "$task_archive"); task_actual=${task_actual%% *}
    [[ "$task_actual" == "$(printf '%s' "$task_sha" | tr 'A-F' 'a-f')" ]] || fail 'Typst archive checksum mismatch'
    # Do not extract archive paths, symlinks or metadata as root: stream only the known binary.
    case "$(uname -m)" in x86_64) task_typst_member=typst-x86_64-unknown-linux-musl/typst;; aarch64|arm64) task_typst_member=typst-aarch64-unknown-linux-musl/typst;; *) fail 'unsupported CPU architecture';; esac
    tar -xJOf "$task_archive" "$task_typst_member" > "$task_work/typst"
    chmod 0755 "$task_work/typst"; task_typst="$task_work/typst"
  fi
  if [[ -z "$task_typst" ]]; then task_typst=$(command -v typst || true); fi
  [[ -n "$task_typst" && -f "$task_typst" && -x "$task_typst" ]] || fail 'provide --typst PATH or a verified --typst-archive'
  if [[ "$task_stage" != true ]]; then
    "$task_binary" version >/dev/null || fail 'searchan executable is incompatible with this host'
    "$task_typst" --version >/dev/null || fail 'Typst executable is incompatible with this host'
    if [[ "$task_deps" == true ]]; then
      command -v apt-get >/dev/null || fail '--install-deps currently supports Debian/Ubuntu apt only'
      apt-get update
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates fonts-noto-cjk
    fi
    "$task_typst" fonts | grep -Eq 'Noto Sans CJK SC|Heiti SC|Songti SC' || fail 'install fonts-noto-cjk or rerun with --install-deps'
  fi
  if [[ -e "$task_release" ]]; then
    cmp -s "$task_binary" "$task_release/searchan" && cmp -s "$task_typst" "$task_release/typst" || fail 'release name exists with different binaries; choose a new version'
  else
    install -d -m 0755 "$task_prefix/releases"
    task_release_stage=$(mktemp -d "$task_prefix/releases/.install-XXXXXX")
    install -m 0755 "$task_binary" "$task_release_stage/searchan"
    install -m 0755 "$task_typst" "$task_release_stage/typst"
    chmod 0755 "$task_release_stage"
    mv "$task_release_stage" "$task_release"
  fi
else
  [[ -x "$task_release/searchan" && -x "$task_release/typst" ]] || fail 'rollback release is not installed'
fi
if [[ "$task_stage" != true ]]; then
  if ! getent group searchan >/dev/null; then groupadd --system searchan; fi
  if ! id searchan >/dev/null 2>&1; then useradd --system --gid searchan --home-dir /var/lib/searchan --shell /usr/sbin/nologin searchan; fi
  [[ $(id -u searchan) != 0 ]] || fail 'searchan service user must not be root'
fi
install -d -m 0755 "$task_prefix/bin" "$task_dest/etc/systemd/system"
install -d -m 0700 "$task_dest/var/lib/searchan"
if [[ -e "$task_config" ]]; then cp "$task_config" "$task_work/config.old"; task_had_config=true; fi
if [[ -e "$task_unit" ]]; then cp "$task_unit" "$task_work/service.old"; task_had_unit=true; fi
if [[ -L "$task_prefix/current" ]]; then task_old_link=$(readlink "$task_prefix/current"); elif [[ -e "$task_prefix/current" ]]; then fail 'current activation is not a symlink'; fi
for task_name in searchan typst; do
  if [[ -e "$task_prefix/bin/$task_name" || -L "$task_prefix/bin/$task_name" ]]; then
    [[ -L "$task_prefix/bin/$task_name" && $(readlink "$task_prefix/bin/$task_name") == "../current/$task_name" ]] || fail "refusing to overwrite unmanaged bin/$task_name"
  else ln -s "../current/$task_name" "$task_prefix/bin/$task_name"; fi
done
if [[ "$task_stage" != true ]] && systemctl is-active --quiet searchan; then task_was_active=true; systemctl stop searchan; fi
task_activated=true
if [[ -n "$task_env" ]]; then install -m 0640 "$task_env" "$task_config"; elif [[ ! -e "$task_config" ]]; then install -m 0640 "$task_source_root/.env.example" "$task_config"; fi
install -m 0644 "$task_source_root/deploy/searchan.service" "$task_unit"
install -m 0755 "$task_source_root/scripts/admin-linux.sh" "$task_prefix/bin/searchan-admin"
atomic_link "releases/$task_version" "$task_prefix/current"
if [[ "$task_stage" != true ]]; then
  chown root:searchan "$task_config"
  chown searchan:searchan "$task_dest/var/lib/searchan"
  systemctl daemon-reload
  if [[ "$task_start" == yes || "$task_start" == auto && "$task_was_active" == true ]]; then
    systemctl enable searchan
    systemctl start searchan
    systemctl is-active --quiet searchan || fail 'service did not become active'
    task_ready=false
    for ((task_attempt=0; task_attempt<10; task_attempt++)); do
      if "$task_prefix/bin/searchan-admin" healthcheck >/dev/null 2>&1; then task_ready=true; break; fi
      systemctl is-active --quiet searchan || break
      sleep 1
    done
    [[ "$task_ready" == true ]] || fail 'service readiness failed after start'
  fi
fi
task_finished=true
if [[ "$task_stage" == true ]]; then
  printf 'Staged release %s. No packages, accounts, services or live checks were changed.\n' "$task_version"
else
  printf 'Installed release %s; config: /etc/searchan.env; data: /var/lib/searchan.\n' "$task_version"
  printf 'After configuring credentials and access: systemctl enable --now searchan\n'
  printf 'Inspect: systemctl status searchan; journalctl -u searchan\n'
fi
