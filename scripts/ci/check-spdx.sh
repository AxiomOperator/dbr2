#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# SPDX header gate (ADR-0013). Called by `make lint` and the CI license job.
#
# Every tracked (or untracked but not ignored) source file below must contain
# "SPDX-License-Identifier: Apache-2.0" in its first 5 lines:
#   *.go *.sql *.proto *.sh Dockerfile* (anywhere)
#   web/src/**/*.{ts,tsx,js,mjs}  web/scripts/**  .github/workflows/*.{yml,yaml}
#
# Skipped: spikes/**, **/node_modules/**, deleted files, lockfiles and *.sum,
# and generated files whose first 5 lines carry the Go generated-code marker
# "// Code generated ... DO NOT EDIT." (sqlc, protoc-gen-go, *.pb.go).
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

wants_header() {
  local f=$1 base=${1##*/}
  case $f in
    spikes/* | */node_modules/* | node_modules/*) return 1 ;;
  esac
  case $base in
    *.sum | package-lock.json | pnpm-lock.yaml | yarn.lock | *.lock) return 1 ;;
  esac
  case $f in
    *.go | *.sql | *.proto | *.sh) return 0 ;;
    web/src/*.ts | web/src/*.tsx | web/src/*.js | web/src/*.mjs) return 0 ;;
    web/scripts/*) return 0 ;;
    .github/workflows/*.yml | .github/workflows/*.yaml) return 0 ;;
  esac
  case $base in
    Dockerfile*) return 0 ;;
  esac
  return 1
}

missing=()
checked=0
while IFS= read -r -d '' f; do
  wants_header "$f" || continue
  [[ -f $f ]] || continue
  top=$(head -n 5 -- "$f")
  if grep -qE '^// Code generated .* DO NOT EDIT\.$' <<<"$top"; then
    continue
  fi
  checked=$((checked + 1))
  grep -qF 'SPDX-License-Identifier: Apache-2.0' <<<"$top" || missing+=("$f")
done < <(git ls-files -z --cached --others --exclude-standard | sort -zu)

if [[ ${#missing[@]} -gt 0 ]]; then
  for f in "${missing[@]}"; do
    if [[ -n ${GITHUB_ACTIONS:-} ]]; then
      echo "::error file=$f,line=1::missing 'SPDX-License-Identifier: Apache-2.0' in the first 5 lines"
    else
      echo "missing SPDX header: $f"
    fi
  done
  echo "${#missing[@]} file(s) lack an SPDX header. Add e.g. '// SPDX-License-Identifier: Apache-2.0' (Go/TS/proto), '-- SPDX-License-Identifier: Apache-2.0' (SQL) or '# SPDX-License-Identifier: Apache-2.0' (shell/Dockerfile/YAML) near the top."
  exit 1
fi
echo "SPDX check passed ($checked file(s))."
