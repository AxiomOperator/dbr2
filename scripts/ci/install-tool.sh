#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Install a pinned CI tool into ./bin (no sudo) and print its path.
#
#   scripts/ci/install-tool.sh <oasdiff|actionlint|govulncheck|buf|go-licenses|gitleaks>
#
# Versions for oasdiff, actionlint, govulncheck and buf come from the root
# Makefile (single source of truth, also used by `make tools`); the others are
# pinned here. Already-installed binaries of the right version are reused.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
bin=$root/bin
mkdir -p "$bin"

# Pinned here (not used by the Makefile).
GO_LICENSES_VERSION=v2.0.1
GITLEAKS_VERSION=v8.30.1

make_var() {
  awk -v k="$1" '$1 == k && $2 == ":=" { print $3; exit }' "$root/Makefile"
}

tool=${1:?usage: $0 <tool>}
case $tool in
  oasdiff) pkg=github.com/oasdiff/oasdiff ver=$(make_var OASDIFF_VERSION) ;;
  actionlint) pkg=github.com/rhysd/actionlint/cmd/actionlint ver=$(make_var ACTIONLINT) ;;
  govulncheck) pkg=golang.org/x/vuln/cmd/govulncheck ver=$(make_var GOVULNCHECK) ;;
  buf) pkg=github.com/bufbuild/buf/cmd/buf ver=$(make_var BUF_VERSION) ;;
  go-licenses) pkg=github.com/google/go-licenses/v2 ver=$GO_LICENSES_VERSION ;;
  gitleaks) pkg=github.com/zricethezav/gitleaks/v8 ver=$GITLEAKS_VERSION ;;
  *) echo "unknown tool: $tool" >&2; exit 2 ;;
esac
[[ -n $ver ]] || { echo "error: no pinned version for $tool" >&2; exit 1; }

stamp=$bin/.$tool.version
if [[ -x $bin/$tool && -f $stamp && $(cat "$stamp") == "$ver" ]]; then
  echo "$bin/$tool"
  exit 0
fi
echo "installing $pkg@$ver into $bin" >&2
GOBIN=$bin go install "$pkg@$ver" >&2
echo "$ver" >"$stamp"
echo "$bin/$tool"
