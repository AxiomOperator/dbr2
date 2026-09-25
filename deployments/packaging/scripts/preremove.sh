#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# RPM %preun of dbr2-agent. $1 = number of dbr2-agent packages left after this
# transaction: 0 = erase, 1 = upgrade (nothing to do; the new %post restarts).
# Must tolerate systemd not running and never fail the transaction.

if [ "${1:-0}" -eq 0 ]; then
  if [ -d /run/systemd/system ]; then
    systemctl --no-reload disable --now dbr2-agent.service >/dev/null 2>&1 || :
  elif command -v systemctl >/dev/null 2>&1; then
    # Offline: still remove the enablement symlinks.
    systemctl --no-reload disable dbr2-agent.service >/dev/null 2>&1 || :
  fi
fi
exit 0
