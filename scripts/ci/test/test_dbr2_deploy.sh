#!/usr/bin/env bash
# shellcheck disable=SC2016 # test snippets are expanded inside in_lib, on purpose
# SPDX-License-Identifier: Apache-2.0
#
# Tests for deployments/docker-compose/dbr2-deploy.sh safety logic. Docker is
# never called: every case sources the script as a library in a subshell,
# stubs the Docker-facing functions and works on a temporary deployment dir.
set -euo pipefail
# shellcheck source=scripts/ci/test/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

DEPLOY="$TEST_ROOT/deployments/docker-compose/dbr2-deploy.sh"

# in_lib <bash snippet> — runs the snippet with the script loaded against a
# fresh deployment dir ($D) holding a .env with a comment and two versions.
in_lib() {
  local d
  d=$(mktemp -d "$WORK/deploy.XXXXXX")
  printf '# keep me\nDBR2_HOSTNAME=backup.example.lan\nDBR2_SERVER_VERSION=0.1.0\nDBR2_WEB_VERSION=0.1.0\n' >"$d/.env"
  DBR2_DEPLOY_LIB=1 DBR2_DEPLOY_DIR="$d" DBR2_DEPLOY_STATE_DIR="$d/.deploy" DBR2_DEPLOY_BACKUP_DIR="$d/backups" \
    D="$d" bash -c "source '$DEPLOY'; set -Eeuo pipefail; $1"
}

# Stubs for the Docker-facing steps of update.
STUBS='
lock() { mkdir -p "$STATE_DIR"; }
cmd_check() { :; }
installed() { return 0; }
wait_idle() { :; }
make_backup() { mkdir -p "$D/backups/b"; printf "%s\n" "$D/backups/b"; }
write_sums() { :; }
apply() { echo x >>"$D/applies"; }
'

run_case "env_set replaces a key and keeps comments and other keys" 0 in_lib '
  env_set DBR2_SERVER_VERSION 0.2.0.61
  env_set DBR2_WORKER_VERSION 0.2.0
  grep -qx "# keep me" "$D/.env" && grep -qx "DBR2_HOSTNAME=backup.example.lan" "$D/.env" &&
  [[ $(env_get DBR2_SERVER_VERSION) == 0.2.0.61 && $(env_get DBR2_WORKER_VERSION) == 0.2.0 ]] &&
  [[ $(grep -c "^DBR2_SERVER_VERSION=" "$D/.env") == 1 ]] && [[ $(stat -c %a "$D/.env") == 600 ]]'
run_case "env_set rejects values that could inject shell or env lines" nonzero in_lib 'env_set DBR2_SERVER_VERSION "0.2; rm -rf /"'

run_case "--version sets every DBR² image" 0 in_lib '
  ALL_VERSION=0.2.0; out=$(target_versions)
  [[ $(grep -c "=0.2.0$" <<<"$out") == 4 ]]'
run_case "--version rejects junk" nonzero in_lib 'ALL_VERSION="latest; x"; target_versions'
run_case "--set overrides one component; defaults come from .env" 0 in_lib '
  SETS=(DBR2_WORKER_VERSION=0.2.0.7); out=$(target_versions)
  grep -qx DBR2_WORKER_VERSION=0.2.0.7 <<<"$out" && grep -qx DBR2_SERVER_VERSION=0.1.0 <<<"$out" && grep -qx DBR2_REPOSERVER_VERSION=0.1.0 <<<"$out"'
run_case "--set refuses keys other than image versions and the registry" nonzero in_lib 'SETS=(DBR2_HOSTNAME=evil); target_versions'
run_case "--channel edge" 0 in_lib 'CHANNEL=edge; [[ $(target_versions | grep -c "=edge$") == 4 ]]'
run_case "--release-manifest maps server, worker, reposerver and web" 0 in_lib '
  printf "%s" "{\"schema\":1,\"components\":{\"server\":{\"version\":\"0.3.0.90\"},\"worker\":{\"version\":\"0.3.1.90\"},\"reposerver\":{\"version\":\"0.2.0.90\"},\"web\":{\"version\":\"0.4.0.90\"},\"agent\":{\"version\":\"0.3.0.90\"}}}" >"$D/m.json"
  MANIFEST="$D/m.json"; out=$(target_versions)
  grep -qx DBR2_SERVER_VERSION=0.3.0.90 <<<"$out" && grep -qx DBR2_WORKER_VERSION=0.3.1.90 <<<"$out" &&
  grep -qx DBR2_REPOSERVER_VERSION=0.2.0.90 <<<"$out" && grep -qx DBR2_WEB_VERSION=0.4.0.90 <<<"$out"'
run_case "a release manifest missing a component is refused" nonzero in_lib '
  printf "%s" "{\"components\":{\"server\":{\"version\":\"0.3.0.90\"}}}" >"$D/m.json"; MANIFEST="$D/m.json"; target_versions'

run_case "refuses to continue when the database volume disappeared (would start an EMPTY platform)" nonzero in_lib '
  mkdir -p "$STATE_DIR"; echo "t	install	ok	x" >"$STATE_DIR/history.tsv"; installed() { return 1; }; check_volumes'
assert "  … with an explanation" output_contains "EMPTY platform"
run_case "install refuses over an existing installation" nonzero in_lib 'lock() { :; }; check_tools() { :; }; installed() { return 0; }; cmd_install'

# The .env check needs the same directory, so run it as one snippet.
run_case "update: failed pull leaves .env byte-identical and never applies" 0 in_lib "$STUBS"'
  ASSUME_YES=1; ALL_VERSION=0.9.0
  pull_or_build() { return 1; }
  cp "$D/.env" "$D/env.orig"
  (cmd_update) && exit 1
  cmp -s "$D/.env" "$D/env.orig" && [[ ! -e "$D/applies" ]]'
run_case "update: unhealthy result rolls the versions back automatically" 0 in_lib "$STUBS"'
  ASSUME_YES=1; ALL_VERSION=0.9.0
  pull_or_build() { :; }
  verify() { [[ -e "$D/verified-once" ]] && return 0; touch "$D/verified-once"; return 1; }
  cp "$D/.env" "$D/env.orig"
  (cmd_update) && exit 1
  cmp -s "$D/.env" "$D/env.orig" && [[ $(wc -l <"$D/applies") == 2 ]] && grep -q "	update	rolled_back	" "$STATE_DIR/history.tsv"'
run_case "update: success writes the new versions and records history" 0 in_lib "$STUBS"'
  ASSUME_YES=1; ALL_VERSION=0.9.0
  pull_or_build() { :; }; verify() { :; }
  cmd_update
  [[ $(env_get DBR2_WORKER_VERSION) == 0.9.0 ]] && grep -q "	update	ok	" "$STATE_DIR/history.tsv" && grep -qx "DBR2_SERVER_VERSION=0.1.0" "$D/backups/b/versions.env"'
run_case "rollback --restore-db needs the typed confirmation even with --yes" nonzero in_lib '
  lock() { :; }; check_tools() { :; }; installed() { return 0; }
  mkdir -p "$D/backups/20260101T000000Z-pre-update"; (cd "$D/backups/20260101T000000Z-pre-update" && : >versions.env && sha256sum versions.env >SHA256SUMS)
  ASSUME_YES=1; RESTORE_DB=1; cmd_rollback </dev/null'
assert "  … and says how to confirm" output_contains "DBR2_DEPLOY_CONFIRM_RESTORE"
LIFE='
lock() { :; }; check_tools() { :; }; check_config() { :; }; check_volumes() { :; }; wait_idle() { echo idle >>"$D/calls"; }
compose() {
  case $1 in
    config) printf "%s\n" postgres valkey temporal temporal-schema temporal-namespace temporal-ui dbr2-server dbr2-worker dbr2-reposerver dbr2-web proxy ;;
    ps) [[ -n ${MISSING:-} ]] || echo c0ffee ;;
    *) echo "$*" >>"$D/compose" ;;
  esac
}
verify() { :; }; wait_healthy() { echo "health $*" >>"$D/calls"; }
'
run_case "start refuses without an installation (would start an empty platform)" nonzero in_lib "$LIFE"'installed() { return 1; }; cmd_start'
run_case "start never recreates or upgrades: up -d --no-build --no-recreate" 0 in_lib "$LIFE"'installed() { return 0; }; cmd_start
  grep -qx "up -d --no-build --no-recreate" "$D/compose"'
run_case "stop keeps containers and volumes (compose stop, never down)" 0 in_lib "$LIFE"'installed() { return 0; }; ASSUME_YES=1; cmd_stop
  grep -qx "stop" "$D/compose" && ! grep -q "down" "$D/compose" && grep -qx idle "$D/calls"'
run_case "stopping only the web console does not wait for backups" 0 in_lib "$LIFE"'installed() { return 0; }; ASSUME_YES=1; SERVICES=(dbr2-web); cmd_stop
  grep -qx "stop dbr2-web" "$D/compose" && [[ ! -e "$D/calls" ]]'
run_case "restart of one service waits for operations and checks only that service" 0 in_lib "$LIFE"'installed() { return 0; }; ASSUME_YES=1; SERVICES=(dbr2-worker); cmd_restart
  grep -qx "restart dbr2-worker" "$D/compose" && grep -qx idle "$D/calls" && grep -qx "health dbr2-worker" "$D/calls"'
run_case "restart after the stack was brought down fails fast and points to start" nonzero in_lib "$LIFE"'installed() { return 0; }; ASSUME_YES=1; MISSING=1; SERVICES=(dbr2-worker); cmd_restart'
assert "  … with guidance" output_contains "use"
run_case "unknown services are refused" nonzero in_lib "$LIFE"'installed() { return 0; }; SERVICES=(nope); cmd_restart'
run_case "one-shot jobs cannot be started on their own" nonzero in_lib "$LIFE"'installed() { return 0; }; SERVICES=(temporal-schema); cmd_start'
run_case "service names are only accepted for start/stop/restart" nonzero bash "$DEPLOY" update dbr2-worker
run_case "the script never contains destructive Docker commands" 0 bash -c "! grep -nE -- '(down -v|--volumes|volume (rm|prune)|system prune|image prune|--remove-orphans)' '$DEPLOY' | grep -v '^[0-9]*:#'"

finish
