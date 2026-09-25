#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Run every scripts/ci/test/test_*.sh. Each test builds throwaway git
# repositories in a temporary directory; nothing in the working tree changes.
set -euo pipefail

dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
failed=()
for t in "$dir"/test_*.sh; do
  echo "== ${t##*/}"
  if ! bash "$t"; then
    failed+=("${t##*/}")
  fi
done
echo
if [[ ${#failed[@]} -gt 0 ]]; then
  echo "FAILED: ${failed[*]}"
  exit 1
fi
echo "All CI script tests passed."
