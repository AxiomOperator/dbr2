#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Userspace NFSv4-only server. CLIENTS env = comma list of allowed client IPs/CIDRs.
set -e
mkdir -p /export /var/run/ganesha /var/lib/nfs/ganesha
cat > /etc/ganesha/ganesha.conf <<CONF
NFS_CORE_PARAM { Protocols = 4; Enable_NLM = false; Enable_RQUOTA = false; NFS_Port = 2049; }
NFSV4 { Graceless = true; Lease_Lifetime = 30; Grace_Period = 30; }
EXPORT_DEFAULTS { Access_Type = None; }
EXPORT {
  Export_Id = 1; Path = /export; Pseudo = /export;
  Protocols = 4; Transports = TCP; SecType = sys;
  Access_Type = None; Squash = No_Root_Squash;
  FSAL { Name = VFS; }
  CLIENT { Clients = ${CLIENTS:-*}; Access_Type = RW; }
}
LOG { Default_Log_Level = EVENT; }
CONF
cat /etc/ganesha/ganesha.conf
exec /usr/bin/ganesha.nfsd -F -L /dev/stdout -f /etc/ganesha/ganesha.conf -N NIV_EVENT
