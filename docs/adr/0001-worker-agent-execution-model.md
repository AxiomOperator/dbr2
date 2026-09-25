# ADR-0001: Worker–agent execution model (Agent Gateway)

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Temporal activities execute in workers that poll Temporal task queues. DBR² Agents run on protected Docker hosts and connect **outbound** over gRPC + mTLS; they must not be reachable inbound, and they must not have access to Temporal. A worker's activity therefore needs a way to reach a specific agent, and there must be a defined behavior when an agent disconnects during an activity.

## Decision

`dbr2-worker` runs all Temporal activities. It reaches agents through an **Agent Gateway**, a function hosted in `dbr2-server`.

```text
Temporal ──► dbr2-worker (activity)
                 │ internal gRPC: Dispatch(agent_id, command)
                 ▼
            Agent Gateway (in dbr2-server)
                 │ long-lived bidirectional gRPC stream (opened by the agent, mTLS)
                 ▼
            dbr2-agent ──► Docker Engine / host filesystem / Repository Server
```

1. **Session:** on startup the agent opens `AgentService.Connect`, a bidirectional stream authenticated by its mTLS certificate identity. The gateway records the session (agent ID, gateway instance, connected-at time, last-seen time) in PostgreSQL as a lease with a heartbeat.
2. **Dispatch:** an activity calls `Dispatch(agent_id, command)` on the gateway. Every command has:
   - `command_id` = Temporal workflow ID + activity ID + attempt, used as an idempotency key
   - `deadline`
   - a typed payload (for example `SnapshotVolume`, `RunHook`, `Quiesce`, `Resume`, `RestoreVolume`)
3. **Progress:** the agent streams progress and a terminal result back on the session. The activity relays progress as Temporal heartbeats, so Temporal detects a stalled activity through its heartbeat timeout.
4. **Routing:** in the MVP, a single `dbr2-server` instance hosts the gateway. When there are several instances, the dispatcher looks up the agent's session lease in PostgreSQL and forwards the command to the owning instance. Routing never depends on Valkey.
5. **Disconnect semantics:**
   - The agent keeps a **local command journal** (`/var/lib/dbr2/agent/journal`). A command that has started keeps running when the stream drops.
   - On reconnect, the agent reports the state of every journaled command. The gateway resolves the waiting dispatch call if it is still waiting.
   - If the agent does not reconnect before the activity's heartbeat timeout, the activity fails and Temporal retries it using the **same `command_id`**. The agent then returns the stored result or resumes the operation instead of starting it again.
   - Safety-critical state, such as a quiesced application, is protected on the agent itself (see ADR-0005) and never depends on the connection surviving.
6. **Backup payloads never pass through the gateway or the worker.** The agent sends data directly to the Repository Server (ADR-0002).

## Consequences

- Agents stay outbound-only and have no Temporal credentials.
- `dbr2-server` holds long-lived connections, so it is stateful with respect to agent sessions. Scaling it out needs the lease-based forwarding described above.
- Idempotent command handling is a hard requirement for every agent command.

## Open items / validation

- Define the protobuf contract (`proto/agent/`) with an explicit command catalog and versioning.
- Test disconnects during each command type in the real-Docker integration suite.

## Spike results (2026-09-25): `spikes/temporal/RESULTS.md`

- Heartbeat timeouts detect dead or stalled activities quickly (about 4 s in the spike), independently of a long StartToClose.
- **Amendments:**
  - Activities that report progress set a **heartbeat timeout longer than the gateway's agent reconnect grace period**, so a brief disconnect does not fail the activity.
  - The **heartbeat payload carries the agent's checkpoint** (the command's progress state). On retry, the activity passes it back in the command, together with the same `command_id`, so the agent can resume.
