#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# dbr2-deploy.sh — install, update and roll back the DBR² Docker Compose
# control plane WITHOUT destroying data.
#
#   ./dbr2-deploy.sh check                      pre-flight checks only
#   ./dbr2-deploy.sh install                    first installation
#   ./dbr2-deploy.sh update [version options]   back up, pull, apply, verify
#   ./dbr2-deploy.sh backup                     database + config backup now
#   ./dbr2-deploy.sh rollback [--restore-db]    previous image versions (and
#                                               optionally the pre-update DB)
#   ./dbr2-deploy.sh status                     versions, health, history
#
# Version options (update): --release-manifest FILE|URL (release-manifest.json
# of a DBR² release), --version V (the same tag for every DBR² image, e.g.
# 0.2.0 or 0.2.0.61), --channel edge (latest main build), --set KEY=VALUE
# (e.g. DBR2_SERVER_VERSION=0.2.0.61). Without one, the current tags are
# pulled again (picks up rebuilt moving tags such as 0.2.0).
#
# Safety rules (never relaxed by any flag):
#   - never `down -v`, never removes volumes, networks, images or containers
#     it did not create; no --remove-orphans, no prune;
#   - refuses to update when the database volume is missing (a wrong project
#     directory or name would otherwise start an EMPTY platform);
#   - refuses to install over an existing installation;
#   - backs up the databases, .env, secrets and Compose files before every
#     update; the database is never restored without a typed confirmation
#     (--yes does not cover it; automation sets
#     DBR2_DEPLOY_CONFIRM_RESTORE="RESTORE DATABASE");
#   - pulls every image before changing anything; a failed update rolls back
#     the image versions automatically (the data stays).
# Common options: --yes (no prompt), --dry-run, --dev (compose.dev.yaml;
# images are built from source instead of pulled), --force (update even
# while backups/restores run), --wait-idle SECONDS (default 900),
# --timeout SECONDS (health wait, default 600).
set -Eeuo pipefail

HERE=${DBR2_DEPLOY_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}
PROJECT=dbr2
STATE_DIR=${DBR2_DEPLOY_STATE_DIR:-$HERE/.deploy}
BACKUP_ROOT=${DBR2_DEPLOY_BACKUP_DIR:-$HERE/backups}
KEEP_BACKUPS=${DBR2_DEPLOY_KEEP_BACKUPS:-5}
MIN_FREE_MB=${DBR2_DEPLOY_MIN_FREE_MB:-2048}
VERSION_KEYS=(DBR2_SERVER_VERSION DBR2_WORKER_VERSION DBR2_REPOSERVER_VERSION DBR2_WEB_VERSION)
# Manifest component name → .env key.
declare -A MANIFEST_KEYS=([server]=DBR2_SERVER_VERSION [worker]=DBR2_WORKER_VERSION [reposerver]=DBR2_REPOSERVER_VERSION [web]=DBR2_WEB_VERSION)
APP_SERVICES=(dbr2-server dbr2-worker dbr2-reposerver dbr2-web proxy)
DATABASES=(dbr2 temporal temporal_visibility)

ASSUME_YES=0 DRY_RUN=0 DEV=0 FORCE=0 RESTORE_DB=0
WAIT_IDLE=900 HEALTH_TIMEOUT=600
MANIFEST="" ALL_VERSION="" CHANNEL=""
SETS=()

# ---- output ---------------------------------------------------------------------

if [[ -t 1 ]]; then B=$'\e[1m' R=$'\e[31m' G=$'\e[32m' Y=$'\e[33m' N=$'\e[0m'; else B="" R="" G="" Y="" N=""; fi
# Messages go to stderr so functions can return values on stdout.
log() {
  printf '%s\n' "$*" >&2
  if [[ -d $STATE_DIR ]]; then printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$STATE_DIR/deploy.log" || true; fi
}
step() { log "${B}==> $*${N}"; }
ok() { log "${G}ok${N}  $*"; }
warn() { log "${Y}warning:${N} $*"; }
die() { log "${R}error:${N} $*"; exit 1; }

usage() { sed -n '4,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

# ---- helpers ----------------------------------------------------------------------

compose() {
  local files=(-f "$HERE/compose.yaml")
  ((DEV)) && files+=(-f "$HERE/compose.dev.yaml")
  docker compose --project-directory "$HERE" -p "$PROJECT" "${files[@]}" "$@"
}

confirm() { # prompt
  ((ASSUME_YES)) && return 0
  [[ -t 0 ]] || die "not a terminal: pass --yes to confirm: $1"
  read -r -p "$1 [y/N] " a
  [[ $a == [yY] || $a == [yY][eE][sS] ]]
}

# env_get KEY [FILE] — value from .env (or FILE); "" when unset.
env_get() {
  local f=${2:-$HERE/.env}
  [[ -f $f ]] || return 0
  sed -n "s/^$1=//p" "$f" | tail -n1
}

# env_set KEY VALUE — replace or append a line in .env, preserving everything else.
env_set() {
  local key=$1 val=$2 tmp
  [[ $val =~ ^[A-Za-z0-9._:/@+-]*$ ]] || die "refusing unsafe value for $key: $val"
  tmp=$(mktemp "$HERE/.env.XXXXXX")
  if grep -q "^$key=" "$HERE/.env"; then
    awk -v k="$key" -v v="$val" 'index($0, k "=") == 1 { print k "=" v; next } { print }' "$HERE/.env" >"$tmp"
  else
    cat "$HERE/.env" >"$tmp"
    printf '%s=%s\n' "$key" "$val" >>"$tmp"
  fi
  chmod 600 "$tmp"
  mv "$tmp" "$HERE/.env"
}

current_versions() { # [ENV_FILE] — prints KEY=value for every version key (default 0.1.0 like compose.yaml)
  local k v
  for k in "${VERSION_KEYS[@]}"; do
    v=$(env_get "$k" "${1:-}")
    printf '%s=%s\n' "$k" "${v:-0.1.0}"
  done
}

volume_exists() { docker volume inspect "${PROJECT}_$1" >/dev/null 2>&1; }

installed() { volume_exists pgdata; }

psql_db() { # db sql — runs as the postgres superuser over the container's local socket
  compose exec -T postgres psql -U postgres -d "$1" -tAc "$2"
}

free_mb() { df -Pm "$1" | awk 'NR==2 {print $4}'; }

lock() {
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  exec 9>"$STATE_DIR/lock"
  flock -n 9 || die "another dbr2-deploy run holds $STATE_DIR/lock"
}

record() { # action result details
  printf '%s\t%s\t%s\t%s\n' "$(date -u +%FT%TZ)" "$1" "$2" "$3" >>"$STATE_DIR/history.tsv"
}

# ---- checks -------------------------------------------------------------------------

check_tools() {
  command -v docker >/dev/null || die "docker is not installed"
  docker info >/dev/null 2>&1 || die "cannot talk to the Docker daemon (are you in the docker group, or root?)"
  local v
  v=$(docker compose version --short 2>/dev/null) || die "Docker Compose v2 is required (docker compose)"
  v=${v#v}
  [[ ${v%%.*} -ge 3 || ( ${v%%.*} -eq 2 && $(cut -d. -f2 <<<"$v") -ge 20 ) ]] || die "Docker Compose >= 2.20 is required (found $v)"
  command -v flock >/dev/null || die "flock (util-linux) is required"
  command -v openssl >/dev/null || die "openssl is required"
  ok "docker $(docker version -f '{{.Server.Version}}' 2>/dev/null), compose $v"
}

check_config() {
  [[ -f $HERE/.env ]] || die ".env is missing — run: $0 install"
  local s
  for s in postgres_password temporal_db_password dbr2_db_password dbr2_database_url dbr2_secret_key dbr2_internal_token; do
    [[ -s $HERE/secrets/$s ]] || die "secret secrets/$s is missing or empty (never regenerate it on an existing installation)"
  done
  [[ -e $HERE/secrets/dbr2_entra_client_secret ]] || die "secrets/dbr2_entra_client_secret is missing (it may be empty)"
  compose config -q || die "the Compose configuration is invalid"
  ok "configuration and secrets present"
  local host
  host=$(env_get DBR2_HOSTNAME)
  [[ -z $host || $host == localhost ]] && ! ((DEV)) && warn "DBR2_HOSTNAME is '${host:-unset}': agents on other hosts cannot reach this server"
  return 0
}

check_storage() {
  local p fstype
  p=$(env_get DBR2_REPO_HOST_PATH)
  p=${p:-/mnt/dbr2-repo}
  if ((DEV)); then
    ok "development mode: Repository storage is the local mock (.dev/repo)"
    return 0
  fi
  [[ -d $p ]] || die "Repository path $p does not exist (mount the NAS share first; see README)"
  fstype=$(findmnt -no FSTYPE --target "$p" 2>/dev/null || true)
  if [[ $(findmnt -no TARGET --target "$p" 2>/dev/null) != "$p" ]]; then
    die "$p is not a mount point: mount the NFS share there (hard, nfs4) before deploying — writing a repository to the local disk would be silently wrong"
  fi
  [[ $fstype == nfs4 ]] || warn "$p is mounted as '$fstype', not nfs4 (dbr2-reposerver's storage guard requires nfs4 unless reconfigured)"
  ok "Repository storage $p mounted ($fstype)"
}

check_space() {
  local dir mb
  mkdir -p "$BACKUP_ROOT"
  for dir in "$BACKUP_ROOT" "$(docker info -f '{{.DockerRootDir}}' 2>/dev/null || echo /var/lib/docker)"; do
    [[ -d $dir ]] || continue
    mb=$(free_mb "$dir" 2>/dev/null || echo 0)
    ((mb >= MIN_FREE_MB)) || die "only ${mb} MiB free on $dir (need ${MIN_FREE_MB} MiB)"
  done
  ok "disk space"
}

check_volumes() {
  if [[ -s $STATE_DIR/history.tsv ]] && ! installed; then
    die "this directory has a deploy history but the volume ${PROJECT}_pgdata does not exist.
       Starting now would create an EMPTY platform. Check the Compose project name/directory,
       or restore the platform first (docs/operations/platform-recovery.md)."
  fi
  if installed; then ok "data volumes present (${PROJECT}_pgdata)"; fi
}

cmd_check() {
  step "Pre-flight checks"
  check_tools
  check_config
  check_storage
  check_space
  check_volumes
}

# ---- versions -----------------------------------------------------------------------

# target_versions — prints KEY=value lines for the requested versions.
target_versions() {
  local -A t=()
  local line k v
  while IFS='=' read -r k v; do t[$k]=$v; done < <(current_versions)
  if [[ -n $CHANNEL ]]; then
    [[ $CHANNEL == edge ]] || die "unknown channel '$CHANNEL' (only 'edge')"
    for k in "${VERSION_KEYS[@]}"; do t[$k]=edge; done
  fi
  if [[ -n $ALL_VERSION ]]; then
    [[ $ALL_VERSION =~ ^[0-9]+\.[0-9]+(\.[0-9]+(\.[0-9]+)?)?$ ]] || die "--version must look like 0.2, 0.2.0 or 0.2.0.61"
    for k in "${VERSION_KEYS[@]}"; do t[$k]=$ALL_VERSION; done
  fi
  if [[ -n $MANIFEST ]]; then
    local json
    if [[ $MANIFEST =~ ^https:// ]]; then
      json=$(curl -fsSL "$MANIFEST") || die "cannot download $MANIFEST"
    else
      json=$(cat "$MANIFEST") || die "cannot read $MANIFEST"
    fi
    local name
    for name in "${!MANIFEST_KEYS[@]}"; do
      v=$(jq -r --arg n "$name" '.components[$n].version // empty' <<<"$json" 2>/dev/null) || die "$MANIFEST is not a DBR² release manifest (jq is required)"
      [[ -n $v ]] || die "release manifest has no version for component '$name'"
      t[${MANIFEST_KEYS[$name]}]=$v
    done
  fi
  for line in ${SETS[@]+"${SETS[@]}"}; do
    k=${line%%=*} v=${line#*=}
    [[ " ${VERSION_KEYS[*]} DBR2_REGISTRY " == *" $k "* ]] || die "--set only accepts ${VERSION_KEYS[*]} DBR2_REGISTRY"
    t[$k]=$v
  done
  for k in "${VERSION_KEYS[@]}"; do printf '%s=%s\n' "$k" "${t[$k]}"; done
  for line in ${SETS[@]+"${SETS[@]}"}; do [[ ${line%%=*} == DBR2_REGISTRY ]] && printf '%s\n' "$line"; done
  return 0
}

# ---- backups ------------------------------------------------------------------------

# make_backup LABEL — prints the backup directory.
make_backup() {
  local label=$1 dir db f
  dir="$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$label"
  mkdir -p "$dir"
  chmod 700 "$BACKUP_ROOT" "$dir"
  compose ps --status running --services 2>/dev/null | grep -qx postgres ||
    die "postgres is not running; cannot take the pre-update database backup (start the stack first, or use install)"
  for db in "${DATABASES[@]}"; do
    f="$dir/$db.dump"
    compose exec -T postgres pg_dump -U postgres -Fc -d "$db" >"$f" || die "pg_dump of $db failed"
    chmod 600 "$f"
    # A dump that pg_restore cannot list is useless: check it now.
    compose exec -T postgres pg_restore -l <"$f" >/dev/null || die "the $db dump is unreadable"
  done
  cp -p "$HERE/.env" "$dir/env"
  cp -p "$HERE/compose.yaml" "$HERE/Caddyfile" "$dir/"
  [[ -f $HERE/compose.dev.yaml ]] && cp -p "$HERE/compose.dev.yaml" "$dir/"
  mkdir -p "$dir/secrets"
  cp -p "$HERE"/secrets/* "$dir/secrets/"
  chmod 700 "$dir/secrets"
  chmod 600 "$dir"/secrets/* "$dir/env"
  current_versions >"$dir/versions.env"
  chmod -R go-rwx "$dir"
  write_sums "$dir"
  prune_backups
  printf '%s\n' "$dir"
}

# write_sums DIR — SHA256SUMS of every file in DIR (checked before a rollback).
write_sums() {
  local tmp
  tmp=$(mktemp)
  (cd "$1" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum) >"$tmp"
  mv "$tmp" "$1/SHA256SUMS"
  chmod 600 "$1/SHA256SUMS"
}

prune_backups() {
  local all
  mapfile -t all < <(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -name '20*' | sort)
  local n=${#all[@]} i
  for ((i = 0; i < n - KEEP_BACKUPS; i++)); do
    rm -rf -- "${all[$i]}" # our own backup directories only (name pattern above)
  done
}

latest_backup() {
  find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -name '20*-pre-update' 2>/dev/null | sort | tail -n1
}

cmd_backup() {
  lock
  step "Backing up databases, .env, secrets and Compose files"
  local dir
  dir=$(make_backup manual)
  ok "backup written to $dir"
  record backup ok "$dir"
}

# ---- apply & verify ------------------------------------------------------------------

busy_operations() { # prints the number of running backups and restores
  local n
  n=$(psql_db dbr2 "SELECT (SELECT count(*) FROM recovery_points WHERE state = 'pending') + \
    (SELECT count(*) FROM restore_runs WHERE state IN ('requested','running'))" 2>/dev/null || echo 0)
  printf '%s\n' "${n//[^0-9]/}"
}

wait_idle() {
  local n waited=0
  n=$(busy_operations)
  [[ ${n:-0} -eq 0 ]] && { ok "no backup or restore is running"; return 0; }
  if ((FORCE)); then
    warn "$n backup/restore operation(s) running; continuing because of --force (running agent commands resume after the restart, quiesced applications are resumed by their agents' dead-man leases)"
    return 0
  fi
  log "waiting up to ${WAIT_IDLE}s for $n running backup/restore operation(s) to finish…"
  while ((waited < WAIT_IDLE)); do
    sleep 10
    waited=$((waited + 10))
    n=$(busy_operations)
    [[ ${n:-0} -eq 0 ]] && { ok "operations finished"; return 0; }
  done
  die "$n operation(s) still running after ${WAIT_IDLE}s; try later or pass --force"
}

wait_healthy() {
  local deadline=$((SECONDS + HEALTH_TIMEOUT)) s id st pending
  while :; do
    pending=()
    for s in postgres temporal "${APP_SERVICES[@]}"; do
      id=$(compose ps -q "$s" 2>/dev/null | head -n1)
      if [[ -z $id ]]; then pending+=("$s:missing"); continue; fi
      st=$(docker inspect -f '{{.State.Status}}/{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id")
      [[ $st == running/healthy || $st == running/none ]] || pending+=("$s:$st")
    done
    ((${#pending[@]} == 0)) && { ok "all services healthy"; return 0; }
    ((SECONDS >= deadline)) && { warn "not healthy after ${HEALTH_TIMEOUT}s: ${pending[*]}"; return 1; }
    sleep 5
  done
}

verify() {
  local out port code host
  wait_healthy || return 1
  out=$(compose exec -T dbr2-server /usr/local/bin/app migrate status 2>&1) || { warn "migrate status failed: $out"; return 1; }
  if grep -qi pending <<<"$out"; then warn "database migrations still pending:\n$out"; return 1; fi
  ok "database migrations applied ($(grep -c . <<<"$out") total)"
  port=$(env_get DBR2_HTTPS_PORT)
  ((DEV)) && port=${DBR2_DEV_HTTPS_PORT:-9443}
  host=$(env_get DBR2_HOSTNAME)
  host=${host:-localhost}
  # Caddy serves its certificate for the hostname: connect to 127.0.0.1 by that name.
  code=$(curl -sk -o /dev/null -w '%{http_code}' --resolve "$host:${port:-443}:127.0.0.1" "https://$host:${port:-443}/api/v1/health/ready" || true)
  [[ $code == 200 ]] || { warn "readiness through the proxy returned HTTP $code"; return 1; }
  ok "API ready through the proxy (port ${port:-443})"
}

pull_or_build() {
  if ((DEV)); then
    step "Building images from source (development)"
    compose build dbr2-server dbr2-worker dbr2-reposerver dbr2-web
  else
    step "Pulling images (nothing is changed until every image is present)"
    compose pull --quiet
  fi
}

apply() {
  step "Applying (docker compose up -d; containers are recreated only where the image or configuration changed)"
  compose up -d --no-build >&2
}

# ---- commands -------------------------------------------------------------------------

cmd_install() {
  lock
  step "Installing DBR²"
  check_tools
  installed && die "an installation already exists (volume ${PROJECT}_pgdata); use '$0 update'"
  [[ -s $STATE_DIR/history.tsv ]] && die "this directory has a deploy history; refusing to install over it"
  "$HERE/init-secrets.sh"
  check_config
  check_storage
  check_space
  local bundles
  bundles=$(env_get DBR2_PLATFORM_BUNDLE_HOST_PATH)
  bundles=${bundles:-$HERE/platform-bundles}
  mkdir -p "$bundles"
  if ! ((DEV)) && [[ $(id -u) -eq 0 ]]; then chown 65532:65532 "$bundles"; fi
  confirm "Install DBR² in $HERE?" || die "aborted"
  ((DRY_RUN)) && { log "dry run: stopping before any change"; return 0; }
  pull_or_build
  if ! ((DEV)); then
    local p
    p=$(env_get DBR2_REPO_HOST_PATH)
    p=${p:-/mnt/dbr2-repo}
    if [[ ! -e $p/.dbr2-repository-id ]]; then
      step "Preparing Repository storage (writes the sentinel; refuses a non-empty or unmounted path)"
      compose run --rm --no-deps dbr2-reposerver init
    fi
  fi
  apply
  if verify; then
    record install ok "$(current_versions | paste -sd, -)"
    ok "DBR² is running"
    log "Next: sign in as $(env_get DBR2_MASTER_ADMIN_USERNAME | sed 's/^$/dbr2-admin/') — the initial password:"
    log "  docker compose -p $PROJECT cp dbr2-server:/var/lib/dbr2/master-admin-initial-password - | tar -xO"
    log "Then create escrow recipients and the Repository (README → Install, step 6)."
  else
    record install failed "$(current_versions | paste -sd, -)"
    die "installation did not become healthy — see 'docker compose -p $PROJECT logs'"
  fi
}

cmd_update() {
  lock
  cmd_check
  installed || die "no installation found (volume ${PROJECT}_pgdata); use '$0 install'"
  local -a target
  mapfile -t target < <(target_versions)
  step "Plan"
  local line k changed=0 cur
  for line in "${target[@]}"; do
    k=${line%%=*}
    cur=$(env_get "$k")
    [[ $k == DBR2_REGISTRY ]] && { log "  $k: ${cur:-ghcr.io/axiomoperator} → ${line#*=}"; changed=1; continue; }
    if [[ ${cur:-0.1.0} != "${line#*=}" ]]; then log "  $k: ${cur:-0.1.0} → ${line#*=}"; changed=1; else log "  $k: ${cur:-0.1.0} (unchanged)"; fi
  done
  ((changed)) || log "  no version change: images are pulled again and changed containers recreated"
  ((DRY_RUN)) && { log "dry run: stopping before any change"; return 0; }
  confirm "Proceed with the update?" || die "aborted"

  # 1. Everything that can fail without touching the running stack.
  local envbak
  envbak=$(mktemp "$STATE_DIR/env.before.XXXXXX")
  cp -p "$HERE/.env" "$envbak"
  for line in "${target[@]}"; do env_set "${line%%=*}" "${line#*=}"; done
  if ! pull_or_build; then
    cp -p "$envbak" "$HERE/.env"
    die "image pull/build failed; .env restored, nothing was changed"
  fi

  # 2. Quiet point and backup.
  wait_idle
  step "Backing up before the update"
  local dir
  dir=$(make_backup pre-update)
  cp -p "$envbak" "$dir/env" # the backup records the versions BEFORE the update
  current_versions "$envbak" >"$dir/versions.env"
  write_sums "$dir"
  ok "backup written to $dir"

  # 3. Apply and verify; roll the images back automatically on failure.
  apply
  if verify; then
    record update ok "$(current_versions | paste -sd, -) backup=$dir"
    rm -f "$envbak"
    ok "update complete"
    return 0
  fi
  warn "the update did not become healthy: rolling back to the previous image versions (data is untouched)"
  cp -p "$envbak" "$HERE/.env"
  rm -f "$envbak"
  apply
  if verify; then
    record update rolled_back "backup=$dir"
    die "update failed and was rolled back to the previous versions. Database migrations of the new version may have been applied;
       they are additive, but if the old version misbehaves run: $0 rollback --restore-db"
  fi
  record update failed "backup=$dir"
  die "update failed and the automatic rollback is not healthy either. Inspect 'docker compose -p $PROJECT logs'.
       The pre-update backup is in $dir ($0 rollback --restore-db restores it)."
}

cmd_rollback() {
  lock
  check_tools
  installed || die "no installation found (volume ${PROJECT}_pgdata)"
  local dir
  dir=$(latest_backup)
  [[ -n $dir ]] || die "no pre-update backup found in $BACKUP_ROOT"
  (cd "$dir" && sha256sum --quiet -c SHA256SUMS) || die "backup $dir fails its checksums"
  step "Rolling back to the versions recorded in $dir"
  diff <(current_versions) "$dir/versions.env" || true
  if ((RESTORE_DB)); then
    warn "--restore-db replaces the DBR² databases with the dumps from $dir.
       Everything recorded after that backup (recovery points, restores, audit events, settings) is lost from the index;
       recovery points still exist in the Repositories and come back with 'dbr2 admin reindex'."
    # --yes does not cover this: type it, or set the variable explicitly.
    local a=${DBR2_DEPLOY_CONFIRM_RESTORE:-}
    if [[ $a != "RESTORE DATABASE" ]]; then
      [[ -t 0 ]] || die "a database restore needs confirmation: type it interactively or set DBR2_DEPLOY_CONFIRM_RESTORE=\"RESTORE DATABASE\""
      read -r -p "Type RESTORE DATABASE to continue: " a
      [[ $a == "RESTORE DATABASE" ]] || die "aborted"
    fi
  else
    confirm "Roll back the image versions (the database stays as it is)?" || die "aborted"
  fi
  ((DRY_RUN)) && { log "dry run: stopping before any change"; return 0; }
  local k v
  while IFS='=' read -r k v; do env_set "$k" "$v"; done <"$dir/versions.env"
  pull_or_build
  if ((RESTORE_DB)); then
    step "Stopping DBR² services (PostgreSQL keeps running)"
    compose stop dbr2-server dbr2-worker temporal temporal-ui dbr2-web
    step "Taking a safety backup of the current databases first"
    local safety
    safety=$(make_backup pre-restore)
    ok "current databases saved to $safety"
    local db
    for db in "${DATABASES[@]}"; do
      step "Restoring $db"
      # Ownership is kept (the roles dbr2/temporal exist): dbr2-server must
      # still own its tables to run migrations.
      compose exec -T postgres pg_restore -U postgres -d "$db" --clean --if-exists --single-transaction \
        <"$dir/$db.dump" || die "restoring $db failed; the safety backup is $safety"
    done
  fi
  apply
  if verify; then
    record rollback ok "from=$dir restore_db=$RESTORE_DB"
    ok "rollback complete"
    ((RESTORE_DB)) && log "Now run 'dbr2 admin reindex --repository <name>' for every Repository (ADR-0003)."
    return 0
  fi
  record rollback failed "from=$dir"
  die "rollback did not become healthy — see 'docker compose -p $PROJECT logs'"
}

cmd_status() {
  check_tools
  step "Services"
  compose ps --format 'table {{.Service}}\t{{.Image}}\t{{.Status}}'
  step "Configured versions (.env)"
  current_versions | sed 's/^/  /'
  step "Data volumes"
  local v
  for v in pgdata server-state reposerver-state caddy-data; do
    if volume_exists "$v"; then echo "  ${PROJECT}_$v present"; else echo "  ${PROJECT}_$v MISSING"; fi
  done
  step "Recent deploys"
  if [[ -s $STATE_DIR/history.tsv ]]; then tail -n 10 "$STATE_DIR/history.tsv" | sed 's/^/  /'; else echo "  (none recorded)"; fi
  step "Backups ($BACKUP_ROOT, newest last)"
  find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -name '20*' 2>/dev/null | sort | sed 's/^/  /' || true
}

# ---- main -----------------------------------------------------------------------------

main() {
  [[ $# -gt 0 ]] || { usage; exit 2; }
  local cmd=$1
  shift
  while [[ $# -gt 0 ]]; do
    case $1 in
      --yes | -y) ASSUME_YES=1 ;;
      --dry-run) DRY_RUN=1 ;;
      --dev) DEV=1 ;;
      --force) FORCE=1 ;;
      --restore-db) RESTORE_DB=1 ;;
      --wait-idle) WAIT_IDLE=$2; shift ;;
      --timeout) HEALTH_TIMEOUT=$2; shift ;;
      --release-manifest) MANIFEST=$2; shift ;;
      --version) ALL_VERSION=$2; shift ;;
      --channel) CHANNEL=$2; shift ;;
      --set) SETS+=("$2"); shift ;;
      -h | --help) usage; exit 0 ;;
      *) die "unknown option $1 (see --help)" ;;
    esac
    shift
  done
  [[ $WAIT_IDLE =~ ^[0-9]+$ && $HEALTH_TIMEOUT =~ ^[0-9]+$ ]] || die "--wait-idle and --timeout take seconds"
  case $cmd in
    check) cmd_check ;;
    install) cmd_install ;;
    update | upgrade) cmd_update ;;
    backup) cmd_backup ;;
    rollback) cmd_rollback ;;
    status) cmd_status ;;
    -h | --help | help) usage ;;
    *) die "unknown command '$cmd' (check, install, update, backup, rollback, status)" ;;
  esac
}

# Sourcing with DBR2_DEPLOY_LIB=1 loads the functions for tests.
if [[ ${DBR2_DEPLOY_LIB:-} != 1 ]]; then main "$@"; fi
