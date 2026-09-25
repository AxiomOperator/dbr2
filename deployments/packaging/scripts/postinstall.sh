#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# RPM %post of dbr2-agent. $1 = number of dbr2-agent packages installed after
# this transaction: 1 = first install, 2 = upgrade. Must tolerate systemd not
# running (containers, image builds, chroots) and never fail the transaction.
# The service is never enabled here: it has nothing to do until enrolled.

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || :
fi

if [ "${1:-1}" -ge 2 ]; then
  # Upgrade: restart only if it is running (try-restart is a no-op otherwise).
  if [ -d /run/systemd/system ]; then
    systemctl try-restart dbr2-agent.service >/dev/null 2>&1 || :
  fi
else
  cat <<'MSG'
dbr2-agent installed. Next steps:
  1. Enroll this host (registration token from the DBR² console or API):
       dbr2-agent enroll --server <host:port> --token <token> --ca-sha256 <hex>
  2. Enable and start the service:
       systemctl enable --now dbr2-agent
  Logs: journalctl -u dbr2-agent
MSG
fi
exit 0
