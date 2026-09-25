#!/usr/bin/env bash
. "$(dirname "$0")/env.sh"
for f in "$W/server.pid" "$SPIKE/.work/q3/server.pid"; do
  [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null && echo "stopped $(cat "$f")"; rm -f "$f"
done
true
