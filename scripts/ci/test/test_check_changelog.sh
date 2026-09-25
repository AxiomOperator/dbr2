#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Tests for scripts/ci/check-changelog.sh.
set -euo pipefail
# shellcheck source=scripts/ci/test/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

check() { "$CI/check-changelog.sh" "$@"; }

# Base repo shared by every case: main with scaffolded components.
new_repo changelog
scaffold_components
mkdir -p cmd/agent internal/obs docs
echo 'package main' >cmd/agent/main.go
echo 'package obs' >internal/obs/obs.go
echo '# docs' >docs/readme.md
commit_all "base"
base=$(git rev-parse HEAD)

branch() { # fresh branch from base
  git checkout -q -B "$1" "$base"
}

# 1. component change without changelog entry -> fail
branch c1
echo '// change' >>cmd/agent/main.go
commit_all "agent change"
run_case "agent change without entry fails" nonzero check "$base" HEAD
assert "failure names the component and changelog" output_contains "cmd/agent/CHANGELOG.md"

# 2. with an [Unreleased] entry -> pass
add_entry cmd/agent/CHANGELOG.md "- Added a thing."
commit_all "agent changelog"
run_case "agent change with Unreleased entry passes" 0 check "$base" HEAD

# 3. entry added outside [Unreleased] -> fail
branch c3
printf '\n## [0.0.9.1] - 2026-01-01\n### Added\n- Old entry.\n' >>cmd/agent/CHANGELOG.md
echo '// change' >>cmd/agent/main.go
commit_all "agent change, entry in released section"
run_case "entry outside [Unreleased] fails" nonzero check "$base" HEAD

# 4. only a heading added under [Unreleased] -> fail
branch c4
add_entry cmd/agent/CHANGELOG.md "### Security"
echo '// change' >>cmd/agent/main.go
commit_all "heading only"
run_case "heading-only addition fails" nonzero check "$base" HEAD

# 5. docs-only change, no label -> pass (no component touched)
branch c5
echo 'more' >>docs/readme.md
commit_all "docs"
run_case "docs-only change passes without label" 0 check "$base" HEAD

# 6. test-only change inside a component: fails without label, passes with it
branch c6
echo 'package main' >cmd/agent/main_test.go
commit_all "agent test"
run_case "component test change without label fails" nonzero check "$base" HEAD
PR_LABELS="bug,no-changelog" run_case "component test change with no-changelog label passes" 0 check "$base" HEAD

# 7. label with a non-eligible file -> label ignored, fails
branch c7
echo '// change' >>cmd/agent/main.go
echo 'x' >>docs/readme.md
commit_all "code + docs"
PR_LABELS="no-changelog" run_case "no-changelog label with code change fails" nonzero check "$base" HEAD
assert "failure explains label is ignored" output_contains "label ignored"

# 8. shared internal/ change: fails without entry, lists affected components, passes with one
branch c8
echo '// change' >>internal/obs/obs.go
commit_all "shared change"
run_case "shared internal/ change without entry fails" nonzero check "$base" HEAD
assert "failure lists affected Go components" output_contains "reposerver -> cmd/reposerver/CHANGELOG.md"
add_entry cmd/worker/CHANGELOG.md "- Uses the new obs helper."
commit_all "worker entry"
run_case "shared internal/ change with a worker entry passes" 0 check "$base" HEAD

# 9. internal/manifest belongs to manifest-schema, not shared code
branch c9
echo 'package manifest' >internal/manifest/m.go
add_entry cmd/server/CHANGELOG.md "- Unrelated entry."
commit_all "manifest change"
run_case "internal/manifest change needs manifest-schema entry" nonzero check "$base" HEAD
assert "failure names manifest-schema" output_contains "manifest-schema"
add_entry internal/manifest/CHANGELOG.md "- New field."
commit_all "manifest entry"
run_case "internal/manifest change with manifest-schema entry passes" 0 check "$base" HEAD

# 10. editing only a changelog -> pass
branch c10
add_entry api/CHANGELOG.md "- Clarified wording."
commit_all "changelog only"
run_case "changelog-only edit passes" 0 check "$base" HEAD

# 11. api/openapi.yaml change requires api entry
branch c11
echo 'openapi: 3.1.0' >api/openapi.yaml
commit_all "spec"
run_case "api/openapi.yaml change without api entry fails" nonzero check "$base" HEAD

# 12. base branch moved on: only the PR's own changes count (merge-base diff)
branch c12
add_entry cmd/agent/CHANGELOG.md "- PR entry."
echo '// pr' >>cmd/agent/main.go
commit_all "pr"
pr=$(git rev-parse HEAD)
git checkout -q -B mainline "$base"
echo '// main moved' >>cmd/worker/VERSION
commit_all "main moves"
run_case "changes on the base branch are not attributed to the PR" 0 check "$(git rev-parse HEAD)" "$pr"

finish
