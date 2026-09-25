#!/usr/bin/env bash
# Q2: create a filesystem repository, server users and the DBR² ACL set (ADR-0002).
set -euo pipefail
. "$(dirname "$0")/env.sh"

rm -rf "$W"; mkdir -p "$W"

# Repository is created/owned by the reposerver identity, which therefore is
# also the maintenance owner (the only identity that runs maintenance/GC).
ksrv repository create filesystem --path="$W/repo" --cache-directory="$W/cache" \
  --override-username=reposerver --override-hostname=dbr2 >/dev/null 2>&1
echo "== maintenance owner:"; ksrv maintenance info | grep -i owner

# Server users: agents authenticate with THESE passwords, never the repo password.
ksrv server user add agent-a@hosta --user-password=pw-agent-a
ksrv server user add agent-b@hostb --user-password=pw-agent-b
ksrv server user add maint@dbr2    --user-password=pw-maint

# Enable ACLs (installs Kopia's defaults), then tighten them.
ksrv server acl enable
echo "== default ACLs after 'server acl enable':"; ksrv server acl list

# Delete the ACL whose line contains ALL given substrings.
del_acl() {
  local line id
  line=$(ksrv server acl list | { f=cat; for p in "$@"; do f="$f | grep -F -- '$p'"; done; eval "$f"; })
  id=$(awk '{print $1}' <<<"$line" | sed 's/^id://')
  [ "$(wc -l <<<"$id")" = 1 ] && [ -n "$id" ] || { echo "ambiguous/no ACL for: $*" >&2; exit 1; }
  ksrv server acl delete --delete "$id"
}

# 1. Default: FULL on own snapshots (allows DELETE)          -> APPEND (create + read only)
del_acl 'user:*@*' 'access:FULL' 'type=snapshot'
ksrv server acl add --user='*@*' --access=APPEND --target='type=snapshot,username=OWN_USER,hostname=OWN_HOST'

# 2. Default: FULL on own policies (agent could rewrite them) -> READ
del_acl 'user:*@*' 'access:FULL' 'type=policy'
ksrv server acl add --user='*@*' --access=READ --target='type=policy,username=OWN_USER,hostname=OWN_HOST'

# 3. Default: FULL on own user record (agent could change its own password,
#    defeating DBR² rotation/revocation)                       -> removed
del_acl 'user:*@*' 'access:FULL' 'type=user'

# 4. Maintenance identity: list/read/delete every snapshot, manage policies.
ksrv server acl add --user='maint@dbr2' --access=FULL --target='type=snapshot'
ksrv server acl add --user='maint@dbr2' --access=FULL --target='type=policy'

echo "== final DBR² ACLs:"; ksrv server acl list
