#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# OpenAPI breaking-change gate (ADR-0015, final_stack "Control Plane").
#
#   scripts/ci/check-api-contract.sh <base-ref>
#
# Compares <base-ref>:api/openapi.yaml with the working-tree api/openapi.yaml
# using the pinned oasdiff (Makefile OASDIFF_VERSION):
#   - `oasdiff breaking --fail-on ERR -f githubactions`: a breaking change fails
#     unless api/VERSION allows it versus <base-ref>
#     (pre-1.0: MINOR or MAJOR bump; from 1.x: MAJOR bump; lib/semver.sh);
#   - `oasdiff changelog -f markdown` is appended to $GITHUB_STEP_SUMMARY
#     (or printed) to help draft the api changelog entry.
# Skips (exit 0) when the base has no api/openapi.yaml.
set -euo pipefail

# shellcheck source=scripts/ci/lib/semver.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/semver.sh"

base=${1:?usage: $0 <base-ref>}
spec=api/openapi.yaml
root=$(git rev-parse --show-toplevel)
cd "$root"

gha() { [[ -n ${GITHUB_ACTIONS:-} ]]; }
note() { if gha; then echo "::notice::$*"; else echo "NOTE: $*"; fi; }
err() { if gha; then echo "::error::$*"; else echo "ERROR: $*"; fi; }

[[ -f $spec ]] || { err "$spec does not exist; run 'make openapi' and commit it"; exit 1; }
if ! git cat-file -e "$base:$spec" 2>/dev/null; then
  note "no $spec in $base; nothing to compare (first version of the contract)."
  exit 0
fi

oasdiff=$(scripts/ci/install-tool.sh oasdiff)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git show "$base:$spec" >"$tmp/base.yaml"

head_ver=$(tr -d '[:space:]' <api/VERSION)
base_ver=$(git show "$base:api/VERSION" 2>/dev/null | tr -d '[:space:]')
base_ver=${base_ver:-0.0.0}

summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}
{
  echo "## OpenAPI changes vs \`$base\`"
  echo
  "$oasdiff" changelog "$tmp/base.yaml" "$spec" -f markdown || echo "_oasdiff changelog failed_"
  echo
} >>"$summary"

rc=0
"$oasdiff" breaking "$tmp/base.yaml" "$spec" --fail-on ERR -f githubactions || rc=$?
if [[ $rc -eq 0 ]]; then
  echo "No breaking API changes."
  exit 0
fi
if breaking_allowed "$base_ver" "$head_ver"; then
  note "breaking API changes allowed: api version bumped $base_ver -> $head_ver."
  exit 0
fi
err "breaking OpenAPI changes require $(required_bump "$base_ver") of api (api/VERSION is $head_ver, base is $base_ver) and an api/CHANGELOG.md entry (ADR-0015)."
exit 1
