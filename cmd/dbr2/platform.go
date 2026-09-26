// SPDX-License-Identifier: Apache-2.0

package main

import "errors"

// errRestorePlatform points to the real command: platform recovery needs
// direct database access on a fresh installation, and this CLI only talks
// to a running API (ADR-0008; docs/operations/platform-recovery.md).
var errRestorePlatform = errors.New(`platform recovery runs on the DBR² server host, not through the API:

  docker compose run --rm --no-deps dbr2-server admin restore-platform \
      --bundle /restore/dbr2-platform-<timestamp>.tar.zst.age \
      --identity /restore/escrow-identity.txt \
      --secrets-dir /restore/secrets --import-reposerver

See docs/operations/platform-recovery.md`)
