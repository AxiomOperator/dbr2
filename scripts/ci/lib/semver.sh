#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Version-bump policy for breaking contract changes (API, agent protocol).
# Sourced by check-api-contract.sh and check-proto.sh.
set -euo pipefail

# breaking_allowed <base MAJOR.MINOR.BUGFIX> <head MAJOR.MINOR.BUGFIX>
# Succeeds when a breaking change is permitted:
#   base MAJOR 0 (pre-1.0, semver 0.x): MAJOR or MINOR increased;
#   base MAJOR >= 1: MAJOR increased.
breaking_allowed() {
  local bM bm hM hm _
  IFS=. read -r bM bm _ <<<"$(tr -d '[:space:]' <<<"$1")"
  IFS=. read -r hM hm _ <<<"$(tr -d '[:space:]' <<<"$2")"
  bM=${bM:-0} bm=${bm:-0} hM=${hM:-0} hm=${hm:-0}
  ((hM > bM)) && return 0
  ((bM == 0 && hM == 0 && hm > bm)) && return 0
  return 1
}

# required_bump <base version>: describes the bump a breaking change needs.
required_bump() {
  local bM
  bM=$(cut -d. -f1 <<<"$1" | tr -d '[:space:]')
  if [[ ${bM:-0} == 0 ]]; then echo "a MINOR (or MAJOR) bump while pre-1.0"; else echo "a MAJOR bump"; fi
}
