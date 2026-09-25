#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Tests for scripts/ci/check-dco.sh.
set -euo pipefail
# shellcheck source=scripts/ci/test/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

check() { "$CI/check-dco.sh" "$@"; }
change() { echo "$RANDOM$RANDOM" >>file.txt; git add -A; }

new_repo dco
change
git commit -q -m "base (unsigned, before the policy)"
base=$(git rev-parse HEAD)

# 1. signed-off commits pass
git checkout -q -b signed "$base"
change; git commit -q -s -m "signed one"
change; git commit -q -s -m "signed two"
run_case "signed-off commits pass" 0 check "$base" HEAD

# 2. an unsigned commit fails
git checkout -q -b unsigned "$base"
change; git commit -q -s -m "signed"
change; git commit -q -m "not signed"
run_case "unsigned commit fails" nonzero check "$base" HEAD
assert "failure names the unsigned commit" output_contains "not signed"

# 3. sign-off by someone other than the author fails; case-insensitive match passes
git checkout -q -b mismatch "$base"
change; git commit -q -m "wrong signer" -m "Signed-off-by: Someone Else <else@example.com>"
run_case "sign-off with a different email fails" nonzero check "$base" HEAD
git checkout -q -b casefold "$base"
change; git commit -q -m "case" -m "Signed-off-by: Test Author <Author@Example.COM>"
run_case "sign-off email match is case-insensitive" 0 check "$base" HEAD

# 4. exempt list in the base exempts listed commits
git checkout -q -b exempt-base "$base"
change; git commit -q -m "legacy unsigned"
legacy=$(git rev-parse HEAD)
mkdir -p .github
printf '# comment\n%s  # legacy\n' "$legacy" >.github/dco-exempt-commits
git add -A; git commit -q -s -m "add exempt list"
base2=$(git rev-parse HEAD)
run_case "exempt list (bootstrap, read from head) exempts listed commit" 0 check "$base" HEAD
git checkout -q -b pr-after-list "$base2"
change; git commit -q -s -m "signed after list"
run_case "exempt list in base, signed PR commit passes" 0 check "$base2" HEAD

# 5. a PR cannot exempt its own commits when the base already has a list
git checkout -q -b self-exempt "$base2"
change; git commit -q -m "sneaky unsigned"
sneaky=$(git rev-parse HEAD)
echo "$sneaky" >>.github/dco-exempt-commits
git add -A; git commit -q -s -m "try to exempt myself"
run_case "PR cannot exempt its own commits (list read from base)" nonzero check "$base2" HEAD

# 6. merge commits are skipped
git checkout -q -b merged "$base"
change; git commit -q -s -m "feature signed"
git checkout -q -b side "$base"
echo other >other.txt; git add -A; git commit -q -s -m "side signed"
git checkout -q merged
git merge -q --no-ff --no-edit side
run_case "merge commit without sign-off is skipped" 0 check "$base" HEAD

# 7. Dependabot commits are skipped
git checkout -q -b bot "$base"
change
GIT_AUTHOR_NAME="dependabot[bot]" GIT_AUTHOR_EMAIL="49699333+dependabot[bot]@users.noreply.github.com" \
  git commit -q -m "deps: bump x"
run_case "dependabot commit without sign-off is skipped" 0 check "$base" HEAD

# 8. empty range passes
run_case "empty range passes" 0 check HEAD HEAD

finish
