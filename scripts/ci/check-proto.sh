#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Protobuf gate (ADR-0015): `buf lint`, and with a base ref `buf breaking`.
#
#   scripts/ci/check-proto.sh [<base-ref>]
#
# A breaking change against <base-ref> fails unless proto/agent/VERSION
# (agent-protocol) was bumped versus <base-ref>: MINOR or MAJOR while the base
# is 0.x, MAJOR from 1.x on (lib/semver.sh). Skips with a notice when
# there is no buf.yaml (or the base has none).
set -euo pipefail

# shellcheck source=scripts/ci/lib/semver.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/semver.sh"

base=${1:-}
root=$(git rev-parse --show-toplevel)
cd "$root"

gha() { [[ -n ${GITHUB_ACTIONS:-} ]]; }
note() { if gha; then echo "::notice::$*"; else echo "NOTE: $*"; fi; }
err() { if gha; then echo "::error::$*"; else echo "ERROR: $*"; fi; }

if [[ ! -f buf.yaml ]]; then
  note "no buf.yaml at the repository root; proto checks skipped."
  exit 0
fi

buf=$(scripts/ci/install-tool.sh buf)
"$buf" lint

[[ -z $base ]] && exit 0
if ! git cat-file -e "$base:buf.yaml" 2>/dev/null; then
  note "no buf.yaml in $base; buf breaking skipped (no baseline)."
  exit 0
fi

sha=$(git rev-parse "$base^{commit}")
head_ver=$(tr -d '[:space:]' <proto/agent/VERSION)
base_ver=$(git show "$base:proto/agent/VERSION" 2>/dev/null | tr -d '[:space:]')
base_ver=${base_ver:-0.0.0}

rc=0
"$buf" breaking --against ".git#ref=$sha" --error-format "$(gha && echo github-actions || echo text)" || rc=$?
if [[ $rc -eq 0 ]]; then
  echo "No breaking protobuf changes."
  exit 0
fi
if breaking_allowed "$base_ver" "$head_ver"; then
  note "breaking protobuf changes allowed: agent-protocol version bumped $base_ver -> $head_ver."
  exit 0
fi
err "breaking protobuf changes require $(required_bump "$base_ver") of agent-protocol (proto/agent/VERSION is $head_ver, base is $base_ver) and a proto/agent/CHANGELOG.md entry (ADR-0015)."
exit 1
