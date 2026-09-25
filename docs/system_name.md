# DBR²

**Docker Backup, Recovery & Restore**

> Aligned with `stack_info/final_stack.md`, which wins on any conflict.

## Components

| Component | Deployable | Implementation (per final stack) |
|---|---|---|
| DBR² Console | `dbr2-web` (web container) | Next.js administrative console |
| DBR² Server | `dbr2-server` | Go control plane (Chi + Huma, OpenAPI) |
| DBR² Worker | `dbr2-worker` | Go Temporal workers running backup, restore, verify and retention workflows |
| DBR² Agent | `dbr2-agent` | Go data-plane service on each protected Docker host (gRPC + mTLS) |
| DBR² CLI | `dbr2` | Go CLI against the REST/OpenAPI API |
| DBR² Repository | — | Repository abstraction, initially backed by Kopia |
| DBR² Recovery Engine | — | Restore workflows (worker) plus restore execution (agent) |

## Binaries and release artifacts

```text
dbr2-server
dbr2-worker
dbr2-agent
dbr2            (CLI)
dbr2-web        (web container)
```

All other docs, examples and commands use these names. Older names (`dockerbackup-*`, `dbk`, `docker-recover`) are retired.
