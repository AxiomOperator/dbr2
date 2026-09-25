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
  - `dbr2-reposerver`: the Kopia repository server. **Amended after the spike:** Kopia's server, ACL and user code lives in `internal/` packages, which cannot be imported. `dbr2-reposerver` therefore runs `server start` **in-process through Kopia's public `cli` package** (verified to behave identically to the upstream binary). User and ACL administration uses the same in-process CLI commands. The fallback, if the `cli` package becomes unusable, is supervising the pinned upstream `kopia` binary. This process also owns maintenance and GC (ADR-0002).
  - `dbr2-worker`: `maint@dbr2` operations (list, delete, set policy, restore). Never maintenance.
  - `dbr2` CLI: offline and agent-only recovery
- **Last-resort recovery:** repositories remain standard Kopia repositories. If DBR² binaries are unavailable, a stock `kopia` CLI of a compatible version, plus the escrowed repository password (ADR-0008), can still restore the data (ADR-0003). The platform runbook documents this procedure.

## Spike results (2026-09-25): `spikes/kopia-library/RESULTS.md`

- **Confirmed** using only public packages: `repo`, `repo/blob/filesystem`, `snapshot`, `snapshot/upload`, `snapshot/policy`, `snapshot/restore`, `snapshot/snapshotfs`, `fs/localfs`, `fs/virtualfs`. Covered: create and connect, tagged path snapshots, `io.Reader` stream snapshots, the manifest snapshot, restore with checksum verification, and progress and cancellation. The same engine wrapper (about 330 lines) worked against the repository server.
- **Pitfalls, which the engine compatibility suite must cover:**
  1. **Restores are shallow by default.** Set `RestoreDirEntryAtDepth = math.MaxInt32`.
  2. **The uploader ignores context cancellation.** Bridge `ctx.Done()` to `Uploader.Cancel()`, and **never save a manifest marked incomplete**.
  3. **Tag keys must not contain `:`**, or the stock CLI cannot filter by them. Use `dbr2-rp`, `dbr2-app`, `dbr2-component` and `dbr2-kind` (ADR-0004).
  4. Restore options from ADR-0006: `IgnorePermissionErrors = false` and sparse writing on.
- A stock `kopia` CLI lists DBR²'s tagged snapshots and prints the recovery manifest, which confirms the ADR-0003 last-resort recovery path.

## Consequences

- Kopia upgrades carry engineering cost, which the compatibility suite keeps safe.
- The Kopia license (Apache-2.0) is compatible with distributing DBR² binaries. Keep a record of third-party notices.

## Open items / validation

- Phase 0 spike: use Kopia as a library to create a repository-server client connection, a tagged snapshot from a path, a snapshot from an `io.Reader`, and a restore. Measure effort and identify which Kopia APIs are needed.
