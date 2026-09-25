# ADR-0007: Kopia integrated as an embedded Go library

- **Status:** Accepted (owner confirmed 2026-09-25)
- **Date:** 2026-09-25

## Context

DBR² uses Kopia as its backup engine. Kopia is written in Go and can be either:

1. imported as a Go library (its packages are public, but the API is not guaranteed stable), or
2. run as a bundled `kopia` CLI binary whose JSON output DBR² parses.

The choice affects the `BackupEngine` interface, agent packaging, progress reporting, cancellation, and whether offline restore needs a separate binary.

## Options

| | Embedded library | Shell out to the CLI |
|---|---|---|
| Deployables | One static binary each for `dbr2-agent`, `dbr2-reposerver` and `dbr2` | Every host also needs a pinned `kopia` binary |
| Progress and heartbeats | Native Go callbacks feed Temporal heartbeats (ADR-0001) | Scrape the CLI's progress output |
| Cancellation | `context.Context` | Kill the process; cleanup is less precise |
| Streaming database dumps and images | Pass an `io.Reader` directly | Pipe through stdin |
| Tagging and manifest control (ADR-0004) | Full programmatic control | Limited to CLI flags |
| API stability | **Unstable:** upgrades may need code changes | CLI and JSON output are more stable |
| Fault isolation | A bug can crash the agent process | Crashes stay in a child process |
| Offline and agent-only restore | `dbr2 recover` works with nothing else installed | Needs the `kopia` binary shipped alongside |

Precedent: other backup products (for example Velero) integrate Kopia as a library.

## Decision

**Embed Kopia as a Go library, pinned to an exact version and isolated behind the `BackupEngine` interface** in a single package (`internal/engine/kopia`).

- **Isolation:** no other package imports Kopia directly. API churn during upgrades is contained in one package.
- **Upgrades are deliberate roadmap items,** each gated by an **engine compatibility suite**. The suite restores fixture repositories created by every previously shipped version.
- **Where it is embedded:**
  - `dbr2-agent`: a repository-server client
  - `dbr2-reposerver`: the Kopia repository server
  - `dbr2-worker`: maintenance-identity operations
  - `dbr2` CLI: offline and agent-only recovery
- **Last-resort recovery:** repositories remain standard Kopia repositories. If DBR² binaries are unavailable, a stock `kopia` CLI of a compatible version, plus the escrowed repository password (ADR-0008), can still restore the data (ADR-0003). The platform runbook documents this procedure.

## Consequences

- Kopia upgrades carry engineering cost, which the compatibility suite keeps safe.
- The Kopia license (Apache-2.0) is compatible with distributing DBR² binaries. Keep a record of third-party notices.

## Open items / validation

- Phase 0 spike: use Kopia as a library to create a repository-server client connection, a tagged snapshot from a path, a snapshot from an `io.Reader`, and a restore. Measure effort and identify which Kopia APIs are needed.
