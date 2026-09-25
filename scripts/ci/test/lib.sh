#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Helpers for the CI script tests. Sourced by scripts/ci/test/test_*.sh.
set -euo pipefail

TEST_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
CI=$TEST_ROOT/scripts/ci
# shellcheck disable=SC2034 # used by the test scripts
RELEASE=$TEST_ROOT/scripts/release
# shellcheck source=scripts/ci/lib/components.sh
source "$CI/lib/components.sh"

TESTS_RUN=0
TESTS_FAILED=0
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Isolate from the user's git configuration.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME="Test Author" GIT_AUTHOR_EMAIL="author@example.com"
export GIT_COMMITTER_NAME="Test Author" GIT_COMMITTER_EMAIL="author@example.com"
unset GITHUB_ACTIONS PR_LABELS DCO_EXEMPT_FILE

# new_repo <name> -> creates and cd's into an empty repo under $WORK
new_repo() {
  local d=$WORK/$1
  rm -rf "$d"
  mkdir -p "$d"
  cd "$d"
  git init -q -b main
  git config commit.gpgsign false
}

changelog_scaffold() { # <name>
  printf '# Changelog — %s\n\n## [Unreleased]\n\n### Added\n- Component scaffold.\n' "$1"
}

# Populate every component dir with VERSION + CHANGELOG.md and a root VERSION/CHANGELOG.
scaffold_components() {
  local n p
  echo 0.1.0 >VERSION
  changelog_scaffold platform >CHANGELOG.md
  for n in $(component_names); do
    p=$(component_field "$n" 2)
    mkdir -p "$p"
    echo 0.1.0 >"$p/VERSION"
    changelog_scaffold "$n" >"$p/CHANGELOG.md"
  done
}

commit_all() { # <message> [extra git commit args...]
  local m=$1
  shift
  git add -A
  git commit -q -m "$m" "$@"
}

# add_entry <changelog> <line>: append an entry right after "## [Unreleased]"'s "### Added".
add_entry() {
  local f=$1 line=$2
  awk -v l="$line" '{ print } /^## \[Unreleased\]/ { u = 1 } u && /^### Added/ && !done { print l; done = 1 }' "$f" >"$f.tmp"
  mv "$f.tmp" "$f"
}

# run_case <description> <expected exit: 0|nonzero> <command...>
run_case() {
  local desc=$1 want=$2 out rc=0
  shift 2
  TESTS_RUN=$((TESTS_RUN + 1))
  out=$("$@" 2>&1) || rc=$?
  if { [[ $want == 0 && $rc -eq 0 ]] || [[ $want == nonzero && $rc -ne 0 ]]; }; then
    echo "  ok   $desc"
  else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    echo "  FAIL $desc (exit $rc, want $want)"
    while IFS= read -r l; do echo "       | $l"; done <<<"$out"
  fi
  LAST_OUTPUT=$out
}

# assert <description> <command...>  (command must succeed)
assert() {
  local desc=$1
  shift
  TESTS_RUN=$((TESTS_RUN + 1))
  if "$@"; then
    echo "  ok   $desc"
  else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    echo "  FAIL $desc"
  fi
}

# output_contains <text>: last run_case output contains text
output_contains() { grep -qF -- "$1" <<<"$LAST_OUTPUT"; }

finish() {
  echo "  $((TESTS_RUN - TESTS_FAILED))/$TESTS_RUN passed"
  [[ $TESTS_FAILED -eq 0 ]]
}
