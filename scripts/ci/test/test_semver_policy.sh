#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Tests for the breaking-change version policy (scripts/ci/lib/semver.sh) used
# by check-api-contract.sh and check-proto.sh.
set -euo pipefail
# shellcheck source=scripts/ci/test/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/ci/lib/semver.sh
source "$CI/lib/semver.sh"

run_case "0.x: MINOR bump allows a breaking change" 0 breaking_allowed 0.1.0 0.2.0
run_case "0.x: MINOR bump with BUGFIX reset allows it" 0 breaking_allowed 0.1.3 0.2.0
run_case "0.x: MAJOR bump to 1.0.0 allows it" 0 breaking_allowed 0.4.2 1.0.0
run_case "0.x: BUGFIX-only bump fails" nonzero breaking_allowed 0.1.0 0.1.1
run_case "0.x: no bump fails" nonzero breaking_allowed 0.1.0 0.1.0
run_case "1.x: MINOR bump fails" nonzero breaking_allowed 1.2.0 1.3.0
run_case "1.x: BUGFIX bump fails" nonzero breaking_allowed 1.2.0 1.2.1
run_case "1.x: MAJOR bump allows it" 0 breaking_allowed 1.2.0 2.0.0
run_case "missing base version is treated as 0.0.0" 0 breaking_allowed "" 0.1.0
assert "pre-1.0 message asks for a MINOR bump" grep -q MINOR <<<"$(required_bump 0.3.1)"
assert "1.x message asks for a MAJOR bump" test "$(required_bump 1.0.0)" = "a MAJOR bump"

finish
