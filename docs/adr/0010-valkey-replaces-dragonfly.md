# ADR-0010: Valkey replaces Dragonfly

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The stack specified Dragonfly for ephemeral state. DBR²'s cache workload is light: UI cache, sessions, rate limiting, job progress, SSE fan-out and expiring tokens. Dragonfly is licensed under BSL 1.1, which complicates distributing a commercial product.

## Decision

Use **Valkey** (BSD-3, Linux Foundation, Redis-compatible) as the ephemeral store. It must never hold correctness-critical state, locks included (ADR-0011). Losing all Valkey data may only cause cache misses.

## Consequences

- Broad Go client support (`valkey-go`, `go-redis`), simpler licensing, and wide operational familiarity.
- Dragonfly's multithreaded throughput is given up; DBR²'s workload does not need it.
