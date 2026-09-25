#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC2016 # single-quoted bash -c bodies expand in the child shell
#
# Tests for scripts/release/finalize-changelogs.sh and release-manifest.sh.
set -euo pipefail
# shellcheck source=scripts/ci/test/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

fin() { "$RELEASE/finalize-changelogs.sh" --root "$d" --date 2026-10-14 "$@"; }

d=$WORK/fin
mkdir -p "$d"
cd "$d"
scaffold_components
# agent: two subsections with entries plus an empty one and a released history.
cat >cmd/agent/CHANGELOG.md <<'EOF'
# Changelog — agent

Intro text.

## [Unreleased]

### Added
- Quiesce lease dead-man switch.

### Changed

### Notes
- Requires agent-protocol >= 0.2.0.0.

## [0.1.0.12] - 2026-09-30
### Added
- First.
EOF
# worker: empty [Unreleased] -> must stay byte-identical.
printf '# Changelog — worker\n\n## [Unreleased]\n### Added\n### Fixed\n\n## [0.1.0.12] - 2026-09-30\n### Added\n- Old.\n' >cmd/worker/CHANGELOG.md
cp cmd/worker/CHANGELOG.md "$WORK/worker.orig"
echo 0.2.0 >proto/agent/VERSION

run_case "first run succeeds" 0 fin --build 57
first=$LAST_OUTPUT
assert "agent reported with BUILD" grep -qx "agent 0.1.0.57" <<<"$first"
assert "contract component uses BUILD 0" grep -qx "agent-protocol 0.2.0.0" <<<"$first"
assert "platform reported last" bash -c '[[ $(tail -n1 <<<"$1") == "platform 0.1.0.57" ]]' _ "$first"
assert "worker (empty Unreleased) not reported" bash -c '! grep -q "^worker " <<<"$1"' _ "$first"
assert "worker changelog untouched" cmp -s cmd/worker/CHANGELOG.md "$WORK/worker.orig"

expected=$(cat <<'EOF'
# Changelog — agent

Intro text.

## [Unreleased]
### Added
### Changed
### Fixed
### Removed
### Security
### Notes

## [0.1.0.57] - 2026-10-14
### Added
- Quiesce lease dead-man switch.

### Notes
- Requires agent-protocol >= 0.2.0.0.

## [0.1.0.12] - 2026-09-30
### Added
- First.
EOF
)
assert "agent entries moved under the release heading, empty subsections dropped" \
  bash -c '[[ "$(cat cmd/agent/CHANGELOG.md)" == "$1" ]] || { diff <(echo "$1") cmd/agent/CHANGELOG.md; exit 1; }' _ "$expected"
assert "platform section lists released components" grep -qF -- '- `agent` 0.1.0.57 ([changelog](cmd/agent/CHANGELOG.md))' CHANGELOG.md
assert "platform section keeps its own entries" bash -c 'awk "/^## \\[0.1.0.57\\]/{on=1} on" CHANGELOG.md | grep -q "Component scaffold"'

snapshot=$WORK/snap
rm -rf "$snapshot"
cp -r "$d" "$snapshot"
run_case "second run succeeds" 0 fin --build 57
assert "second run reports nothing" test -z "$LAST_OUTPUT"
assert "second run changes nothing (idempotent)" diff -r "$d" "$snapshot"

# A later release with a new agent entry only.
add_entry cmd/agent/CHANGELOG.md "- Another."
run_case "later release succeeds" 0 fin --build 60
assert "later release only reports agent and platform" bash -c '[[ "$1" == "$(printf "agent 0.1.0.60\nplatform 0.1.0.60")" ]]' _ "$LAST_OUTPUT"

# Contract component with entries but unchanged VERSION -> error (BUILD is always 0).
add_entry proto/agent/CHANGELOG.md "- New RPC."
run_case "contract entries without a VERSION bump fail" nonzero fin --build 61
assert "error explains the VERSION bump" output_contains "bump its VERSION"

# release-manifest.sh
printf 'agent 0.1.0.60\nplatform 0.1.0.60\n' >"$WORK/released.txt"
run_case "release manifest renders" 0 "$RELEASE/release-manifest.sh" --root "$d" --build 60 --changed "$WORK/released.txt" --base-commit abc
m=$LAST_OUTPUT
assert "manifest platform version" bash -c '[[ $(jq -r .platform.version <<<"$1") == 0.1.0.60 ]]' _ "$m"
assert "manifest lists all 11 components" bash -c '[[ $(jq -r ".components | length" <<<"$1") == 11 ]]' _ "$m"
assert "manifest marks agent released, db-schema BUILD 0" \
  bash -c '[[ $(jq -r ".components.agent.released, .components[\"db-schema\"].version" <<<"$1" | paste -sd" ") == "true 0.1.0.0" ]]' _ "$m"

run_case "bad --build is rejected" nonzero fin --build x

finish
