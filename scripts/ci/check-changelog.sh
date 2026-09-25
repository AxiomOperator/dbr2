#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# ADR-0015 changelog gate.
#
#   scripts/ci/check-changelog.sh <base-sha> <head-sha>
#
# For every component whose path changed between merge-base(base, head) and
# head, the component's CHANGELOG.md must gain at least one entry line (a
# non-blank line that is not a heading) inside its "## [Unreleased]" section.
#
# Shared Go code (internal/** except internal/manifest/**, go.mod, go.sum)
# affects server, worker, agent, reposerver and cli: at least one of those
# changelogs must gain an entry.
#
# Bypass: the PR label "no-changelog" (read from $PR_LABELS, comma or newline
# separated) passes the check only when every changed file is test, CI or docs:
# docs/**, .github/**, scripts/ci/**, **/*_test.go, **/testdata/**, tests/**,
# spikes/**, or *.md outside component directories. Otherwise the label is
# ignored and the normal check runs.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/ci/lib/components.sh
source "$here/lib/components.sh"

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <base-sha> <head-sha>" >&2
  exit 2
fi
base=$1
head=$2
label=no-changelog

gha() { [[ -n ${GITHUB_ACTIONS:-} ]]; }
err() { if gha; then echo "::error::$*"; else echo "ERROR: $*"; fi; }
note() { if gha; then echo "::notice::$*"; else echo "NOTE: $*"; fi; }

mb=$(git merge-base "$base" "$head") || {
  err "cannot find a merge base of $base and $head (is the checkout shallow? use fetch-depth: 0)"
  exit 2
}

mapfile -t changed < <(git diff --name-only --no-renames "$mb" "$head")
if [[ ${#changed[@]} -eq 0 ]]; then
  echo "No changed files between $mb and $head."
  exit 0
fi

# ---- bypass label --------------------------------------------------------
is_bypassable() {
  local f=$1
  case $f in
    docs/* | .github/* | scripts/ci/* | tests/* | spikes/*) return 0 ;;
    *_test.go | */testdata/* | testdata/*) return 0 ;;
  esac
  if [[ $f == *.md && -z $(component_for_path "$f") ]]; then
    return 0
  fi
  return 1
}

has_label=false
if [[ -n ${PR_LABELS:-} ]]; then
  while IFS= read -r l; do
    l=$(echo "$l" | xargs)
    [[ $l == "$label" ]] && has_label=true
  done < <(tr ',' '\n' <<<"$PR_LABELS")
fi

if $has_label; then
  blockers=()
  for f in "${changed[@]}"; do is_bypassable "$f" || blockers+=("$f"); done
  if [[ ${#blockers[@]} -eq 0 ]]; then
    note "'$label' label present and every changed file is test/CI/docs: changelog check bypassed."
    exit 0
  fi
  err "'$label' label ignored: it is only allowed when every changed file is test, CI or docs. Not eligible:"
  printf '  %s\n' "${blockers[@]}"
  echo "Running the normal changelog check."
fi

# ---- which components changed ---------------------------------------------
declare -A touched=()
for f in "${changed[@]}"; do
  c=$(component_for_path "$f")
  [[ -z $c ]] && continue
  # Editing only the changelog itself does not require another entry.
  if [[ $c != shared-go ]] && [[ $f == "$(component_field "$c" 2)/CHANGELOG.md" ]]; then
    continue
  fi
  touched[$c]+="$f"$'\n'
done

if [[ ${#touched[@]} -eq 0 ]]; then
  echo "No component files changed; no changelog entry required."
  exit 0
fi

# added_unreleased_entries <changelog path> -> prints added entry lines in [Unreleased]
added_unreleased_entries() {
  local file=$1 start end
  if ! git cat-file -e "$head:$file" 2>/dev/null; then
    return 0
  fi
  # Line range of the [Unreleased] section in the head version.
  read -r start end < <(git show "$head:$file" | awk '
    /^## \[Unreleased\]/ { s = NR; next }
    s && !e && /^## / { e = NR - 1 }
    END { if (s) print s, (e ? e : NR); else print 0, 0 }')
  [[ $start -eq 0 ]] && return 0
  git diff -U0 --no-renames "$mb" "$head" -- "$file" | awk -v s="$start" -v e="$end" '
    /^@@/ {
      # @@ -a,b +c,d @@
      split($3, p, ","); n = substr(p[1], 2) + 0; h = 1; next
    }
    !h { next }
    /^\+/ {
      line = substr($0, 2)
      if (n > s && n <= e && line !~ /^[[:space:]]*$/ && line !~ /^#/) print line
      n++; next
    }'
}

fail=0
report_missing() {
  local comp=$1 files=$2 path=$3
  fail=1
  err "component '$comp' changed but $path/CHANGELOG.md has no new entry under '## [Unreleased]' (ADR-0015)."
  echo "  Changed files:"
  printf '%s' "$files" | sed 's/^/    /'
  echo "  Add a line such as '- Describe the change.' under ### Added/Changed/Fixed/... in $path/CHANGELOG.md."
}

for comp in $(printf '%s\n' "${!touched[@]}" | sort); do
  if [[ $comp == shared-go ]]; then
    ok=""
    for g in "${DBR2_SHARED_GO_CONSUMERS[@]}"; do
      p=$(component_field "$g" 2)
      if [[ -n $(added_unreleased_entries "$p/CHANGELOG.md") ]]; then ok=$g; break; fi
    done
    if [[ -n $ok ]]; then
      echo "ok   shared Go code (internal/, go.mod) -> entry found in '$ok' changelog"
    else
      fail=1
      err "shared Go code changed (internal/** outside internal/manifest, go.mod or go.sum) but none of the affected components has a new [Unreleased] entry."
      echo "  Changed files:"
      printf '%s' "${touched[$comp]}" | sed 's/^/    /'
      echo "  Record the change in each affected component's changelog (at least one required):"
      for g in "${DBR2_SHARED_GO_CONSUMERS[@]}"; do
        echo "    $g -> $(component_field "$g" 2)/CHANGELOG.md"
      done
    fi
    continue
  fi
  p=$(component_field "$comp" 2)
  if [[ -n $(added_unreleased_entries "$p/CHANGELOG.md") ]]; then
    echo "ok   $comp -> $p/CHANGELOG.md"
  else
    report_missing "$comp" "${touched[$comp]}" "$p"
  fi
done

if [[ $fail -ne 0 ]]; then
  echo
  echo "Changelog check failed. If this PR changes only tests, CI or docs, add the '$label' label and re-run this job."
  exit 1
fi
echo "Changelog check passed."
