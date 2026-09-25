#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Developer Certificate of Origin gate (ADR-0013).
#
#   scripts/ci/check-dco.sh <base-sha> <head-sha>
#
# Every non-merge commit in base..head must carry a "Signed-off-by:" trailer
# whose email matches the commit's author email (case-insensitive). Create
# signed commits with `git commit -s`.
#
# Exemptions: full commit SHAs listed one per line in .github/dco-exempt-commits
# ("#" starts a comment). The list is read from the BASE commit so a PR cannot
# exempt its own commits; if the base has no such file (bootstrap), the list in
# HEAD is used. Override the path with $DCO_EXEMPT_FILE (tests only).
#
# Bot authors that cannot sign off (Dependabot) are skipped; they are listed in
# bot_authors below. A maintainer still reviews and merges those PRs.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <base-sha> <head-sha>" >&2
  exit 2
fi
base=$1
head=$2
exempt_path=.github/dco-exempt-commits
bot_authors=" 49699333+dependabot[bot]@users.noreply.github.com "

gha() { [[ -n ${GITHUB_ACTIONS:-} ]]; }
err() { if gha; then echo "::error::$*"; else echo "ERROR: $*"; fi; }

declare -A exempt=()
load_exempt() {
  local line sha
  while IFS= read -r line || [[ -n $line ]]; do
    line=${line%%#*}
    sha=$(echo "$line" | xargs)
    [[ -z $sha ]] && continue
    if [[ ! $sha =~ ^[0-9a-f]{40}$ ]]; then
      echo "warning: ignoring malformed entry '$sha' in DCO exempt list (full 40-char SHA required)" >&2
      continue
    fi
    exempt[$sha]=1
  done
}
if [[ -n ${DCO_EXEMPT_FILE:-} ]]; then
  [[ -f $DCO_EXEMPT_FILE ]] && load_exempt <"$DCO_EXEMPT_FILE"
elif git cat-file -e "$base:$exempt_path" 2>/dev/null; then
  load_exempt < <(git show "$base:$exempt_path")
elif git cat-file -e "$head:$exempt_path" 2>/dev/null; then
  echo "note: $exempt_path not in base; using the copy from head (bootstrap)."
  load_exempt < <(git show "$head:$exempt_path")
fi

mapfile -t commits < <(git rev-list --no-merges --reverse "$base..$head")
if [[ ${#commits[@]} -eq 0 ]]; then
  echo "No commits to check in $base..$head."
  exit 0
fi

bad=0
for c in "${commits[@]}"; do
  subject=$(git log -1 --format=%s "$c")
  if [[ -n ${exempt[$c]:-} ]]; then
    echo "skip ${c:0:12} (exempt) $subject"
    continue
  fi
  author_email=$(git log -1 --format=%ae "$c" | tr '[:upper:]' '[:lower:]')
  if [[ $bot_authors == *" $author_email "* ]]; then
    echo "skip ${c:0:12} (bot author $author_email) $subject"
    continue
  fi
  signoffs=$(git log -1 --format='%(trailers:key=Signed-off-by,valueonly,unfold)' "$c" | tr '[:upper:]' '[:lower:]')
  if [[ -z $signoffs ]]; then
    bad=1
    err "commit ${c:0:12} ('$subject') has no Signed-off-by trailer."
    continue
  fi
  if grep -qF "<$author_email>" <<<"$signoffs"; then
    echo "ok   ${c:0:12} $subject"
  else
    bad=1
    err "commit ${c:0:12} ('$subject'): no Signed-off-by matches author email <$author_email>. Found: $(echo "$signoffs" | paste -sd ';' -)"
  fi
done

if [[ $bad -ne 0 ]]; then
  cat <<'MSG'

DCO check failed. Every commit must be signed off by its author (ADR-0013):
  git commit -s                      # new commits
  git rebase --signoff <base>        # sign off existing commits, then force-push
MSG
  exit 1
fi
echo "DCO check passed (${#commits[@]} commit(s))."
