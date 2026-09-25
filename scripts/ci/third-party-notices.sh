#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Generate THIRD_PARTY_NOTICES and enforce the dependency license policy
# (ADR-0013).
#
#   scripts/ci/third-party-notices.sh            # (re)write THIRD_PARTY_NOTICES
#   scripts/ci/third-party-notices.sh --check    # fail if the committed file is stale
#   scripts/ci/third-party-notices.sh --out F    # write (or --check) F instead
#
# Sources:
#   Go   - production (non-test) dependencies of ./... for linux/amd64, via the
#          pinned github.com/google/go-licenses/v2 `report` with a template.
#   Web  - production dependencies of web/ from web/package-lock.json (entries
#          not marked dev/devOptional; platform-specific optional packages are
#          listed for every platform so the output is machine-independent).
#          License texts are read from web/node_modules (run `make web-install`
#          first); optional packages are listed without text.
# License texts and NOTICE files are de-duplicated: each distinct text is
# printed once, followed by the packages it applies to.
#
# Policy (both ecosystems; SPDX expressions with OR/AND are evaluated):
#   allowed: Apache-2.0 MIT BSD-2-Clause BSD-3-Clause ISC 0BSD Unlicense CC0-1.0
#            Zlib BlueOak-1.0.0 CC-BY-4.0
#   warning: MPL-2.0 (file-level copyleft; allowed, but review modifications)
#   denied:  GPL*, AGPL*, LGPL*, SSPL*, BUSL*
#   anything else (including unknown) fails.
# Reviewed exceptions: scripts/ci/license-exceptions (one "<go|npm>:<name glob>"
# per line with a "# justification" comment). Exceptions are printed as warnings.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"
out=THIRD_PARTY_NOTICES
check=false
while [[ $# -gt 0 ]]; do
  case $1 in
    --check) check=true; shift ;;
    --out) out=$2; shift 2 ;;
    *) echo "usage: $0 [--check] [--out FILE]" >&2; exit 2 ;;
  esac
done

gha() { [[ -n ${GITHUB_ACTIONS:-} ]]; }
err() { if gha; then echo "::error::$*"; else echo "ERROR: $*"; fi; }
warn() { if gha; then echo "::warning::$*"; else echo "WARNING: $*"; fi; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/texts"

# ---- policy ----------------------------------------------------------------
allowed_ids=" Apache-2.0 MIT BSD-2-Clause BSD-3-Clause ISC 0BSD Unlicense CC0-1.0 Zlib BlueOak-1.0.0 CC-BY-4.0 "
warn_ids=" MPL-2.0 "

exceptions=()
if [[ -f scripts/ci/license-exceptions ]]; then
  while IFS= read -r line || [[ -n $line ]]; do
    line=${line%%#*}
    line=$(echo "$line" | xargs)
    [[ -n $line ]] && exceptions+=("$line")
  done <scripts/ci/license-exceptions
fi

# id_verdict <spdx id> -> ok | warn | deny | unknown
id_verdict() {
  local id=${1%+}
  id=${id%-only}
  case $id in
    GPL* | AGPL* | LGPL* | SSPL* | BUSL*) echo deny; return ;;
  esac
  [[ $allowed_ids == *" $id "* ]] && { echo ok; return; }
  [[ $warn_ids == *" $id "* ]] && { echo warn; return; }
  echo unknown
}

# expr_verdict <spdx expression> -> best verdict over OR alternatives, worst over AND terms
expr_verdict() {
  local expr=$1 alt term v best="" worst
  expr=${expr//(/ }
  expr=${expr//)/ }
  rank() { case $1 in ok) echo 0 ;; warn) echo 1 ;; unknown) echo 2 ;; deny) echo 3 ;; esac; }
  while IFS= read -r alt; do
    worst=ok
    for term in ${alt// AND / }; do
      [[ $term == AND || $term == WITH || -z $term ]] && continue
      v=$(id_verdict "$term")
      (($(rank "$v") > $(rank "$worst"))) && worst=$v
    done
    if [[ -z $best ]] || (($(rank "$worst") < $(rank "$best"))); then best=$worst; fi
  done < <(sed 's/ OR /\n/g; s/ or /\n/g' <<<"$expr")
  echo "${best:-unknown}"
}

policy_fail=0
check_policy() { # <eco> <name> <version> <license>
  local eco=$1 name=$2 ver=$3 lic=$4 v ex
  v=$(expr_verdict "$lic")
  case $v in
    ok) return ;;
    warn) warn "$eco:$name@$ver is licensed $lic (weak copyleft; allowed, review any modifications)"; return ;;
  esac
  for ex in "${exceptions[@]}"; do
    # shellcheck disable=SC2053 # glob match is intended
    if [[ $eco:$name == $ex ]]; then
      warn "$eco:$name@$ver is licensed '$lic' ($v) - allowed by scripts/ci/license-exceptions"
      return
    fi
  done
  policy_fail=1
  if [[ $v == deny ]]; then
    err "$eco:$name@$ver uses a denied license: $lic"
  else
    err "$eco:$name@$ver has an unrecognised license: '$lic' (add a reviewed entry to scripts/ci/license-exceptions if acceptable)"
  fi
}

# add_text <file> <label>: record a license/NOTICE text and who it applies to.
add_text() {
  local f=$1 label=$2 h
  [[ -f $f ]] || return 0
  h=$(sed 's/[[:space:]]*$//' "$f" | sha256sum | cut -c1-16)
  [[ -f $tmp/texts/$h.txt ]] || sed 's/[[:space:]]*$//' "$f" >"$tmp/texts/$h.txt"
  echo "$label" >>"$tmp/texts/$h.users"
}

# ---- Go ----------------------------------------------------------------------
: >"$tmp/go.tsv"
if [[ -f go.mod ]]; then
  gol=$(scripts/ci/install-tool.sh go-licenses)
  cat >"$tmp/go.tpl" <<'EOF'
{{range .}}{{.Name}}	{{.Version}}	{{.LicenseName}}	{{.LicensePath}}
{{end}}
EOF
  module=$(go list -m)
  # ./... would also match Go packages vendored inside web/node_modules.
  mapfile -t pkgs < <(go list -e -f '{{.ImportPath}}' ./... | grep -v '/node_modules/')
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$gol" report "${pkgs[@]}" --ignore "$module" \
    --template "$tmp/go.tpl" 2>"$tmp/go.err" >"$tmp/go.raw" || {
    cat "$tmp/go.err" >&2
    err "go-licenses report failed"
    exit 1
  }
  grep -v '^[[:space:]]*$' "$tmp/go.raw" | LC_ALL=C sort -u >"$tmp/go.tsv" || true
fi

declare -A go_lic=() go_ver=() go_path=()
while IFS=$'\t' read -r name ver lic path; do
  [[ -z $name ]] && continue
  go_ver[$name]=$ver
  go_path[$name]=$path
  if [[ -n ${go_lic[$name]:-} ]]; then go_lic[$name]+=" AND $lic"; else go_lic[$name]=$lic; fi
done <"$tmp/go.tsv"

{
  for name in "${!go_lic[@]}"; do printf '%s\n' "$name"; done
} | LC_ALL=C sort >"$tmp/go.names"

while IFS= read -r name; do
  [[ -z $name ]] && continue
  lic=${go_lic[$name]}
  check_policy go "$name" "${go_ver[$name]}" "$lic"
  label="$name ${go_ver[$name]:-(unversioned)} ($lic)"
  if [[ -n ${go_path[$name]} && -f ${go_path[$name]} ]]; then
    add_text "${go_path[$name]}" "$label"
    for n in "$(dirname "${go_path[$name]}")"/NOTICE*; do
      [[ -f $n ]] && add_text "$n" "$label [NOTICE]"
    done
  fi
done <"$tmp/go.names"

# ---- Web ---------------------------------------------------------------------
: >"$tmp/web.tsv"
if [[ -f web/package-lock.json ]]; then
  jq -r '
    .packages | to_entries[]
    | select(.key != "" and (.key | startswith("node_modules/")) and (.value.link | not)
             and (.value.dev | not) and (.value.devOptional | not))
    | [ (.key | sub("^.*node_modules/"; "")),
        (.value.version // ""),
        ((.value.license | if type == "object" then .type else . end) // (.value.licenses // [] | map(.type? // .) | join(" OR ")) // ""),
        (if .value.optional then "optional" else "required" end),
        .key ]
    | @tsv' web/package-lock.json | LC_ALL=C sort -u >"$tmp/web.tsv"
  [[ -d web/node_modules ]] || warn "web/node_modules missing: web license texts omitted (run 'make web-install')"
fi

while IFS=$'\t' read -r name ver lic opt key; do
  [[ -z $name ]] && continue
  [[ -z $lic ]] && lic=UNKNOWN
  check_policy npm "$name" "$ver" "$lic"
  [[ $opt == optional ]] && continue
  label="$name $ver ($lic)"
  dir=web/$key
  for f in "$dir"/LICEN[CS]E* "$dir"/licen[cs]e* "$dir"/COPYING* "$dir"/NOTICE*; do
    [[ -f $f ]] || continue
    case ${f##*/} in NOTICE*) add_text "$f" "$label [NOTICE]" ;; *) add_text "$f" "$label" ;; esac
  done
done <"$tmp/web.tsv"

# ---- render ------------------------------------------------------------------
render() {
  cat <<'EOF'
THIRD-PARTY SOFTWARE NOTICES — DBR² (Docker Backup, Recovery & Restore)

DBR² is licensed under the Apache License 2.0 (see LICENSE and NOTICE).
It incorporates the third-party components listed below, each under its own
license. This file is generated by scripts/ci/third-party-notices.sh — do not
edit it by hand; regenerate it when dependencies change.

EOF
  echo "================================================================================"
  echo "Go modules (dbr2-server, dbr2-worker, dbr2-agent, dbr2-reposerver, dbr2)"
  echo "================================================================================"
  if [[ -s $tmp/go.names ]]; then
    while IFS= read -r name; do
      printf '%s %s — %s\n' "$name" "${go_ver[$name]:-(unversioned)}" "${go_lic[$name]}"
    done <"$tmp/go.names"
  else
    echo "(none)"
  fi
  echo
  echo "================================================================================"
  echo "npm production dependencies (dbr2-web)"
  echo "================================================================================"
  if [[ -s $tmp/web.tsv ]]; then
    while IFS=$'\t' read -r name ver lic opt _; do
      printf '%s %s — %s%s\n' "$name" "$ver" "${lic:-UNKNOWN}" "$([[ $opt == optional ]] && echo ' (optional, platform-specific)')"
    done <"$tmp/web.tsv"
  else
    echo "(none)"
  fi
  echo
  echo "================================================================================"
  echo "License and notice texts"
  echo "================================================================================"
  # Order groups by their first (sorted) user for a stable file.
  for u in "$tmp"/texts/*.users; do
    [[ -f $u ]] || continue
    printf '%s\t%s\n' "$(LC_ALL=C sort -u "$u" | head -n1)" "${u%.users}"
  done | LC_ALL=C sort | while IFS=$'\t' read -r _ base; do
    echo
    echo "--------------------------------------------------------------------------------"
    echo "Applies to:"
    LC_ALL=C sort -u "$base.users" | sed 's/^/  - /'
    echo "--------------------------------------------------------------------------------"
    cat "$base.txt"
  done
}
render >"$tmp/notices"

if [[ $policy_fail -ne 0 ]]; then
  err "dependency license policy failed (see errors above; policy in scripts/ci/third-party-notices.sh)"
fi

if $check; then
  if [[ ! -f $out ]] || ! cmp -s "$tmp/notices" "$out"; then
    err "$out is stale or missing. Run scripts/ci/third-party-notices.sh (after make web-install) and commit the result."
    diff -u "$out" "$tmp/notices" 2>/dev/null | head -n 60 || true
    exit 1
  fi
  echo "$out is up to date."
else
  cp "$tmp/notices" "$out"
  echo "wrote $out"
fi
exit "$policy_fail"
