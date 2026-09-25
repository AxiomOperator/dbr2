#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Move each changelog's [Unreleased] entries under a versioned heading (ADR-0015).
#
#   scripts/release/finalize-changelogs.sh --build <n> [--date YYYY-MM-DD] [--root DIR]
#
# For every component (scripts/ci/lib/components.sh) whose CHANGELOG.md has
# entries under "## [Unreleased]" (non-blank, non-heading lines), the entries
# move to "## [<full-version>] - <date>" (empty ### subsections dropped) and a
# fresh, empty [Unreleased] template is left in place. Full version is
# <VERSION>.<build>, or <VERSION>.0 for contract components.
#
# The platform changelog (root CHANGELOG.md, root VERSION) is finalized when it
# has entries or any component was finalized; its release section lists the
# component versions released with it.
#
# Stdout: one "<name> <full-version>" line per finalized changelog (components
# first, then "platform"). Idempotent: a changelog whose [Unreleased] section is
# empty is left untouched, so a second run changes nothing. A changelog with
# entries whose target version heading already exists is an error (its VERSION
# must be bumped; contract components always release with BUILD 0).
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/ci/lib/components.sh
source "$here/../ci/lib/components.sh"

build="" date=$(date -u +%F) root=.
while [[ $# -gt 0 ]]; do
  case $1 in
    --build) build=$2; shift 2 ;;
    --date) date=$2; shift 2 ;;
    --root) root=$2; shift 2 ;;
    *) echo "usage: $0 --build <n> [--date YYYY-MM-DD] [--root DIR]" >&2; exit 2 ;;
  esac
done
[[ $build =~ ^(0|[1-9][0-9]*)$ ]] || { echo "error: --build must be a non-negative integer" >&2; exit 2; }
[[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || { echo "error: --date must be YYYY-MM-DD" >&2; exit 2; }

# finalize <file> <version> <date> [extra-markdown]
# exit 0 = finalized, 1 = nothing to do, 2 = error
finalize() {
  local file=$1 version=$2 day=$3 extra=${4:-} tmp
  if [[ ! -f $file ]]; then
    echo "error: $file not found" >&2
    return 2
  fi
  if ! grep -q '^## \[Unreleased\]' "$file"; then
    echo "error: $file has no '## [Unreleased]' heading" >&2
    return 2
  fi
  # Any entry lines (non-blank, non-heading) in [Unreleased]?
  if [[ -z $extra ]] && ! awk '
      /^## \[Unreleased\]/ { s = 1; next }
      s && /^## / { exit }
      s && $0 !~ /^[[:space:]]*$/ && $0 !~ /^#/ { found = 1; exit }
      END { exit !found }' "$file"; then
    return 1
  fi
  if grep -qF "## [$version]" "$file"; then
    echo "error: $file has unreleased entries but already contains a [$version] section; bump its VERSION (contract components always use BUILD 0)" >&2
    return 2
  fi
  tmp=$(mktemp)
  local rc=0
  EXTRA=$extra awk -v ver="$version" -v day="$day" '
    function is_entry(l) { return l !~ /^[[:space:]]*$/ && l !~ /^#/ }
    BEGIN { state = 0; nb = 0; extra = ENVIRON["EXTRA"] }   # 0 before, 1 inside [Unreleased], 2 after
    state == 0 && /^## \[Unreleased\]/ { state = 1; next }
    state == 0 { pre[++npre] = $0; next }
    state == 1 && /^## / { state = 2 }
    state == 1 { body[++nb] = $0; next }
    { post[++npost] = $0 }
    END {
      entries = 0
      for (i = 1; i <= nb; i++) if (is_entry(body[i])) entries++
      if (entries == 0 && extra == "") exit 3

      # Released body: keep ### subsections that contain entries.
      out = ""; sec = ""; secn = 0
      for (i = 1; i <= nb + 1; i++) {
        l = (i <= nb) ? body[i] : "### __end__"
        if (l ~ /^###/) {
          if (secn > 0) out = out (out == "" ? "" : "\n") sec
          sec = l; secn = 0; continue
        }
        if (is_entry(l)) { sec = (sec == "" ? l : sec "\n" l); secn++ }
      }
      if (extra != "") out = out (out == "" ? "" : "\n") extra

      for (i = 1; i <= npre; i++) print pre[i]
      print "## [Unreleased]"
      print "### Added"
      print "### Changed"
      print "### Fixed"
      print "### Removed"
      print "### Security"
      print "### Notes"
      print ""
      print "## [" ver "] - " day
      # Blank line before each ### subsection for readability.
      n = split(out, lines, "\n")
      for (i = 1; i <= n; i++) {
        if (lines[i] ~ /^###/ && i > 1) print ""
        print lines[i]
      }
      if (npost > 0) print ""
      for (i = 1; i <= npost; i++) print post[i]
    }' "$file" >"$tmp" || rc=$?
  if [[ $rc -eq 3 ]]; then
    rm -f "$tmp"
    return 1
  elif [[ $rc -ne 0 ]]; then
    rm -f "$tmp"
    return 2
  fi
  # Collapse runs of blank lines and drop trailing blank lines.
  cat -s "$tmp" | awk '{ a[NR] = $0 } END { n = NR; while (n > 0 && a[n] == "") n--; for (i = 1; i <= n; i++) print a[i] }' >"$file"
  rm -f "$tmp"
  return 0
}

released=()
for name in $(component_names); do
  path=$(component_field "$name" 2)
  v=$(full_version "$name" "$build" "$root")
  rc=0
  finalize "$root/$path/CHANGELOG.md" "$v" "$date" || rc=$?
  case $rc in
    0) echo "$name $v"; released+=("$name|$path|$v") ;;
    1) ;;
    *) exit 1 ;;
  esac
done

pv=$(full_version platform "$build" "$root")
extra=""
if [[ ${#released[@]} -gt 0 ]]; then
  extra="### Components"
  for r in "${released[@]}"; do
    IFS='|' read -r n p v <<<"$r"
    extra+=$'\n'"- \`$n\` $v ([changelog]($p/CHANGELOG.md))"
  done
fi
rc=0
finalize "$root/CHANGELOG.md" "$pv" "$date" "$extra" || rc=$?
case $rc in
  0) echo "platform $pv" ;;
  1) ;;
  *) exit 1 ;;
esac
