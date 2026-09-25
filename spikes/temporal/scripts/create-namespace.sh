#!/bin/sh
set -eu
A="${TEMPORAL_ADDRESS:-temporal:7233}"; NS="${NAMESPACE:-dbr2}"
i=0; until temporal operator cluster health --address "$A"; do i=$((i+1)); [ $i -ge 60 ] && exit 1; sleep 2; done
temporal operator namespace describe -n "$NS" --address "$A" >/dev/null 2>&1 \
  || temporal operator namespace create -n "$NS" --retention 72h --address "$A"
echo "namespace $NS ready"
