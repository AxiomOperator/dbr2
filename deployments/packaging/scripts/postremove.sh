#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# RPM %postun of dbr2-agent. $1 = 0 on erase, 1 on upgrade. Reloads systemd
# so it forgets (erase) or re-reads (upgrade) the unit.
#
# Configuration (/etc/dbr2) and state (/var/lib/dbr2/agent: private key,
# certificates, command journal) are deliberately NOT deleted, so an
# accidental removal does not lose the host's identity. To purge, see
# /usr/share/doc/dbr2-agent/README.md ("Uninstall and purge").

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || :
fi
exit 0
