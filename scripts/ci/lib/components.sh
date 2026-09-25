#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Component map (ADR-0015). Sourced by scripts/ci/* and scripts/release/*.
# Keep in sync with internal/version and docs/adr/0015.
set -euo pipefail

# name|path|kind   (kind: contract = BUILD fixed at 0; built = BUILD is run_number)
# shellcheck disable=SC2034
DBR2_COMPONENTS=(
  "api|api|contract"
  "server|cmd/server|built"
  "worker|cmd/worker|built"
  "agent|cmd/agent|built"
  "reposerver|cmd/reposerver|built"
  "cli|cmd/dbr2|built"
  "web|web|built"
  "agent-protocol|proto/agent|contract"
  "manifest-schema|internal/manifest|contract"
  "db-schema|db|contract"
  "deployment|deployments|built"
)

# Go components that consume shared code under internal/ (and go.mod/go.sum).
# shellcheck disable=SC2034 # used by the scripts that source this file
DBR2_SHARED_GO_CONSUMERS=(server worker agent reposerver cli)

# component_field <name> <1|2|3>  -> name, path or kind
component_field() {
  local entry
  for entry in "${DBR2_COMPONENTS[@]}"; do
    IFS='|' read -r n p k <<<"$entry"
    if [[ $n == "$1" ]]; then
      case $2 in 1) echo "$n" ;; 2) echo "$p" ;; 3) echo "$k" ;; esac
      return 0
    fi
  done
  return 1
}

component_names() {
  local entry
  for entry in "${DBR2_COMPONENTS[@]}"; do echo "${entry%%|*}"; done
}

# component_for_path <repo-relative file>  -> component name, "shared-go", or nothing.
# Longest path prefix wins (internal/manifest/ beats internal/).
component_for_path() {
  local f=$1 entry best="" bestlen=0 n p k
  for entry in "${DBR2_COMPONENTS[@]}"; do
    IFS='|' read -r n p k <<<"$entry"
    if [[ $f == "$p/"* ]] && ((${#p} > bestlen)); then
      best=$n
      bestlen=${#p}
    fi
  done
  if [[ -n $best ]]; then
    echo "$best"
  elif [[ $f == internal/* || $f == go.mod || $f == go.sum ]]; then
    echo "shared-go"
  fi
}

# full_version <name> <build> [root]  -> MAJOR.MINOR.BUGFIX.BUILD (BUILD 0 for contracts).
# name "platform" reads the root VERSION.
full_version() {
  local name=$1 build=$2 root=${3:-.} path kind v
  if [[ $name == platform ]]; then
    path=. kind=built
  else
    path=$(component_field "$name" 2) kind=$(component_field "$name" 3)
  fi
  v=$(tr -d '[:space:]' <"$root/$path/VERSION")
  if [[ ! $v =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "error: $path/VERSION holds '$v', expected MAJOR.MINOR.BUGFIX (ADR-0015)" >&2
    return 1
  fi
  [[ $kind == contract ]] && build=0
  echo "$v.$build"
}
