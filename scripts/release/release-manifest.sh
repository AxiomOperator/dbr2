#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Print the platform release manifest (ADR-0015) as JSON.
#
#   scripts/release/release-manifest.sh --build <n> [--root DIR] [--changed FILE] [--base-commit SHA]
#
# --changed takes the stdout of finalize-changelogs.sh ("<name> <version>" per
# line); those components are marked "released": true and get a tag.
# --base-commit records the commit the release was cut from (the manifest is
# committed in the release commit itself, so it cannot hold that commit's SHA).
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/ci/lib/components.sh
source "$here/../ci/lib/components.sh"

build="" root=. changed="" commit=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --build) build=$2; shift 2 ;;
    --root) root=$2; shift 2 ;;
    --changed) changed=$2; shift 2 ;;
    --base-commit) commit=$2; shift 2 ;;
    *) echo "usage: $0 --build <n> [--root DIR] [--changed FILE] [--base-commit SHA]" >&2; exit 2 ;;
  esac
done
[[ $build =~ ^(0|[1-9][0-9]*)$ ]] || { echo "error: --build must be a non-negative integer" >&2; exit 2; }

declare -A rel=()
if [[ -n $changed && -f $changed ]]; then
  while read -r n _; do [[ -n $n ]] && rel[$n]=1; done <"$changed"
fi

pv=$(full_version platform "$build" "$root")
components='{}'
for name in $(component_names); do
  path=$(component_field "$name" 2)
  kind=$(component_field "$name" 3)
  v=$(full_version "$name" "$build" "$root")
  released=false
  [[ -n ${rel[$name]:-} ]] && released=true
  components=$(jq -c --arg n "$name" --arg v "$v" --arg p "$path" --arg k "$kind" \
    --argjson r "$released" \
    '. + {($n): {version: $v, path: $p, contract: ($k == "contract"), released: $r, tag: ($n + "/v" + $v)}}' \
    <<<"$components")
done

jq -n --arg pv "$pv" --arg build "$build" --arg commit "$commit" \
  --arg date "$(date -u +%FT%TZ)" --argjson c "$components" '{
    schema: 1,
    platform: {version: $pv, tag: ("v" + $pv)},
    build: ($build | tonumber),
    base_commit: $commit,
    created: $date,
    components: $c
  }'
