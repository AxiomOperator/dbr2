# Spike: Kopia v0.23.1 as a library, repository server ACLs, and the server-mode data path

- **Date:** 2026-09-25
- **Kopia:** `github.com/kopia/kopia v0.23.1`, pinned in `go.mod`. The CLI was built with `GOBIN=./bin go install github.com/kopia/kopia@v0.23.1`.
- **Go:** 1.26.8, linux/amd64
- **Covers:** ADR-0002 (repository server), ADR-0004 (recovery point model), ADR-0007 (embedded library)
- **Everything ran against real code.** Raw logs are in `evidence/`. The Kopia source references are to the v0.23.1 module in `$GOMODCACHE/github.com/kopia/kopia@v0.23.1/`, abbreviated to `K/` below.

| # | Question | Verdict |
|---|---|---|
| Q1 | Can Kopia be embedded as a Go library for tagged, streamed and cancellable snapshots and restores? | **Confirmed.** Only public packages are needed. There are 3 API pitfalls, listed below. |
| Q2 | Can a repository server with ACLs give "agents append their own snapshots, no delete, no cross-host access"? | **Partial.** Manifest-level ACLs work as intended. There are two real gaps: content reads by object ID are not scoped per user, and agents can trigger retention on the server. The server itself cannot be imported as a Go API. |
| Q3 | Where do splitting, hashing, compression and encryption happen in server mode? Does dedup avoid re-sending? | **Confirmed, with a surprise.** Splitting, hashing and dedup checks run on the client, so unchanged data is not re-sent. **Compression and encryption run on the server, so the wire carries uncompressed data.** |

---

## Q1: Kopia as an embedded Go library (ADR-0007)

**Question.** Can a standalone Go program do the following using only public Kopia v0.23.1 packages?

- create or connect a repository
- take tagged directory snapshots and list them by tag
- take a snapshot from an `io.Reader`
- write a tagged recovery-manifest snapshot
- restore and verify
- report progress and cancel through a context

**Method.** `internal/engine/engine.go` (about 330 lines) is the only file that imports Kopia. It stands in for the Phase-1 `internal/engine/kopia` package. `cmd/q1` drives steps a–f against a filesystem repository in `.work/q1`.

The check in step (f) that no snapshot was persisted after the cancel compares the count of snapshot manifests. On-disk repository size is only logged.

**Evidence** (`evidence/q1.log`: all 10 checks pass):

```text
(a) connected: *repo.directRepository user=agent-a host=hosta
(b) list by tag dbr2-rp=rp_01SPIKE… -> 1 snapshot(s); list all -> 2      PASS
(c) stream snapshot bytes=8388608                                        (8 MiB fake pg_dump through io.Pipe)
(d) PASS: RP is committed: exactly one manifest snapshot found by tag; RP has 3 snapshots
(e) PASS restored tree sha256 matches (25 files); PASS stream sha256 matches; PASS manifest round-trips
(f) progress: hashed=70 MiB … -> cancel() -> "snapshot returned after 73ms … err=upload: context canceled"
    PASS no snapshot manifest persisted for canceled run (4 before, 4 after)
    post-cancel full snapshot ok (320 MiB) ; PASS post-cancel snapshot restores intact
```

The stock CLI reads what the library wrote (`evidence/q1-cli-compat.log`). `kopia snapshot list --tags=dbr2-kind:manifest` finds the manifest snapshot, and `kopia show <root>/recovery-manifest.json` prints the JSON.

**Kopia packages and APIs used.** All are public.

| Need | API |
|---|---|
| Create or connect a repository | `repo.Initialize`, `repo.Connect`, `repo/blob/filesystem.New`, `repo.Open` |
| Connect to a repository server | `repo.ConnectAPIServer(ctx, cfg, &repo.APIServerInfo{BaseURL, TrustedServerCertificateFingerprint}, userPassword, &repo.ConnectOptions{ClientOptions{Username, Hostname}})` |
| Write session | `repo.WriteSession` |
| Snapshot a directory | `fs/localfs.Directory` → `snapshot/upload.NewUploader(w).Upload(ctx, entry, policyTree, sourceInfo, previous...)` → `snapshot.SaveSnapshot` |
| Policy tree | `snapshot/policy.TreeForSource` |
| Snapshot a stream | `fs/virtualfs.NewStaticDirectory("x", []fs.Entry{virtualfs.StreamingFileFromReader(name, rc)})` (the same approach `kopia snapshot create --stdin-file` uses) |
| Tags | `snapshot.Manifest.Tags` is stored as manifest labels. The CLI convention is `"tag:<key>"`. |
| List by tag | `snapshot.ListSnapshotManifests(ctx, rep, src, labels)` + `snapshot.LoadSnapshots` |
| Restore | `snapshot/snapshotfs.SnapshotRoot` → `snapshot/restore.Entry(ctx, rep, &restore.FilesystemOutput{…}, root, restore.Options{…})` |
| Read a stream back | `fs.Directory.Child` → `fs.File.Open` |
| Progress | Implement `upload.Progress`: embed `upload.NullUploadProgress` and override `HashedBytes`, `FinishedFile` and `Enabled`. Also set `repo.WriteSessionOptions.OnUpload` for bytes sent. |
| Cancel | `Uploader.Cancel()` |

**Is anything only available in `internal/`?** Nothing that the agent or restore path needs.

Only the server side is internal:

- `internal/server`
- `internal/acl`
- `internal/auth`
- `internal/user`
- `internal/logfile`

See Q2.

**Pitfalls found (these belong in Phase-1 code and tests):**

1. **Restores are shallow by default.**
   - Problem: `restore.Options{}` has `RestoreDirEntryAtDepth: 0`, which restores only top-level `*.kopia-entry` placeholders. The first run restored 13 placeholder files instead of 25 real files.
   - Fix: set `RestoreDirEntryAtDepth: math.MaxInt32`, which is the CLI default.
2. **The uploader ignores `ctx`.**
   - Problem: it only polls its own cancel flag (`K/snapshot/upload/upload.go:1245 Cancel`, `IsCanceled` checks at lines 400, 687 and 808). Cancellation sometimes surfaces as `context canceled` from a repository write and sometimes as a *successful* return with `IncompleteReason="canceled"`. Both happened across runs.
   - Fix: bridge the context with `context.AfterFunc(ctx, u.Cancel)`, and never save a manifest whose `IncompleteReason != ""`.
   - Side effect: pack blobs written before the cancel stay as unreferenced data until maintenance runs. The next run re-uploaded them in full (320 MiB).
3. **Tag keys must not contain `:`.**
   - Problem: the CLI parses `--tags k:v` with `SplitN(s, ":", 2)` (`K/cli/command_snapshot_create.go:187`). ADR-0004's `dbr2:rp=<id>` would become key `dbr2`, value `rp:<id>`, which a stock CLI cannot filter on.
   - The spike used `dbr2-rp`, `dbr2-app`, `dbr2-component` and `dbr2-kind`, stored as labels `tag:dbr2-rp`, and so on.

**Also noted:**

- `snapshot list` shows Kopia retention annotations (`latest-1..10`) on DBR² snapshots.
- Kopia's default global retention policy (keep-latest 10, and so on) *will* prune DBR² snapshots whenever something applies retention. The stock CLI does this after every `snapshot create`. See Q2 for the server-side variant.

**Conclusion: Confirmed.** About 330 lines of wrapper code covered every ADR-0007 validation item. That includes a repository-server client connection, which Q2 and Q3 exercised with the same `engine` package: snapshot, list, restore and delete over gRPC.

**Proposed ADR impact.**

- **ADR-0007:** Keep the embedded-library decision for the agent, the `dbr2` CLI, and the worker's API-client operations. Add the three pitfalls above as engine-compatibility-suite tests:
  - full-depth restore
  - cancel leaves no manifest
  - tags visible to the stock CLI
- **ADR-0004:** Change the tag spelling from `dbr2:rp=` to colon-free keys: `dbr2-rp`, `dbr2-app`, `dbr2-component` and `dbr2-kind`, stored as `tag:<key>` labels. This keeps them usable from a stock `kopia` CLI (ADR-0003 and ADR-0007 last-resort recovery).
- **ADR-0004 also:** State that DBR² snapshots must be excluded from Kopia's own retention (see Q2).

---

## Q2: Kopia Repository Server and ACLs (ADR-0002)

### Q2a. Can the server be embedded?

**Not as a Go API.**

`evidence/q2-internal-import.log`:

```text
probes/internalimport/main.go:8:2: use of internal package github.com/kopia/kopia/internal/acl not allowed
```

The server is `K/internal/server`. ACLs, auth and users are `internal/acl`, `internal/auth` and `internal/user`.

**However, the public `github.com/kopia/kopia/cli` package can be run in-process.** `cmd/embedded-kopia` is 20 lines and does what upstream `main.go` does: `cli.NewApp().Attach(kingpin)`.

Built as `bin/embedded-kopia` and run as `embedded-kopia server start …`, it served the same repository. The full ACL matrix below gave **identical results** (`evidence/q2-acl-matrix-embedded.log`, 28 checks, the same 3 mismatches).

Limitations of that route:

- It is driven by argv, not by a typed API.
- `--log-dir` and file logging come from `internal/logfile`, so they are unavailable. The script drops that flag.
- The `cli` package can call `os.Exit`.
- The `cli` package is even less of a stable API than `repo` and `snapshot`.
- User and ACL administration (`server user add`, `server acl add`) is also only reachable through `cli` or the binary.

### Q2b and Q2c. Server, users and ACLs

**Method.**

- `scripts/q2-setup.sh` creates the repository as `reposerver@dbr2`, which makes it the maintenance owner. It then adds the users `agent-a@hosta`, `agent-b@hostb` and `maint@dbr2`, runs `server acl enable`, and rewrites the ACLs.
- `scripts/q2-server.sh` runs `kopia server start --address=https://127.0.0.1:51515 --tls-generate-cert --no-ui`.
- `cmd/q2` is a library client, one `repo.ConnectAPIServer` connection per identity. It runs the matrix.
- `scripts/q2-cli.sh` repeats the key claims with the stock CLI as client, then tests deletion and GC.

**Kopia's defaults after `kopia server acl enable` are not usable for DBR².** Every user gets **FULL** on its own snapshots (it can delete them), on its own policies, and on its own user record (it can change its own password).

**The final DBR² ACL set** (`evidence/q2-setup.log`):

```text
user:*@*        access:APPEND target:type=content                                   (default, kept)
user:*@*        access:READ   target:type=policy,policyType=global                  (default, kept)
user:*@*        access:READ   target:type=policy,hostname=OWN_HOST,policyType=host  (default, kept)
user:*@*        access:APPEND target:type=snapshot,username=OWN_USER,hostname=OWN_HOST   (was FULL)
user:*@*        access:READ   target:type=policy,username=OWN_USER,hostname=OWN_HOST     (was FULL)
                (default FULL on type=user,username=OWN_USER@OWN_HOST deleted)
user:maint@dbr2 access:FULL   target:type=snapshot
user:maint@dbr2 access:FULL   target:type=policy
```

Commands: `kopia server acl delete --delete <id>` and `kopia server acl add --user=… --access=… --target=…`.

ACLs are a union of grants, and the highest level wins (`K/internal/acl/acl_manager.go:51 EffectivePermissions`). There are no deny rules.

Snapshot manifest operations need:

- **READ** to get or find (`K/internal/server/grpc_session.go:334,384`)
- **APPEND** to put (`:352`)
- **FULL** to delete (`:427`)

Content needs READ or APPEND on `type=content`, which has **no user or host labels** (`:256`, `:283`, and `K/internal/acl/acl.go allowedLabelsForType`).

**Results from the library client** (`evidence/q2-acl-matrix.log`), trimmed:

| identity | operation | DBR² intent | actual | error / detail |
|---|---|---|---|---|
| agent-a | connect with wrong password | DENY | DENY | `rpc error: code = PermissionDenied desc = access denied for agent-a@hosta` |
| agent-a | connect with server-user password (no repo password) | ALLOW | ALLOW | `*repo.grpcRepositoryClient` |
| agent-a / agent-b | create snapshot of own source | ALLOW | ALLOW | |
| agent-a | list: sees own / sees agent-b's | ALLOW / DENY | ALLOW / DENY | agent-b's snapshots are filtered out of `FindManifests` |
| agent-a | load agent-b manifest by ID | DENY | DENY | `unable to find manifest entries: access denied` |
| agent-a | restore own snapshot | ALLOW | ALLOW | |
| agent-a | **read agent-b root directory and FILE by object ID** | DENY | **ALLOW** | file=data.bin bytes=262144. Content ACLs are global. |
| agent-a | read an unknown object ID | DENY | DENY | `object not found`. Object IDs are not enumerable: there is no list-contents RPC. |
| agent-a | delete own snapshot / agent-b's snapshot | DENY | DENY | `access denied` |
| agent-a | forge a manifest for the agent-b@hostb source | DENY | DENY | `error putting manifest: access denied` |
| agent-a | set retention policy on own source | DENY | DENY | `error writing policy manifest: access denied` |
| agent-a | read global policy (the uploader needs it) | ALLOW | ALLOW | |
| agent-a | enumerate users / write an ACL manifest | DENY | DENY | 0 visible / `access denied` |
| agent-a | **trigger server-side retention on own source (12 unpinned)** | DENY | **ALLOW** | **deleted=2, remaining=10** (default keep-latest=10) |
| agent-a | trigger server-side retention on own source (12 **pinned**) | DENY | DENY | deleted=0 remaining=12 |
| agent-a | trigger server-side retention on agent-b's path | DENY | DENY | The server binds the request to the caller's user@host. agent-b's snapshots were untouched. |
| maint | list all agents' snapshots / restore agent-b's / set policy / delete agent-b's | ALLOW | ALLOW | |
| agent-a, maint | connection is direct (so could run maintenance) | DENY | DENY | Both are `grpcRepositoryClient`. |

**The same claims from the stock CLI as client** (`evidence/q2-cli.log`):

```text
[agent-a] snapshot create …            exit=0
[agent-a] snapshot delete <own> --delete   error deleting …: error deleting manifest: access denied   exit=1
[agent-a] snapshot delete <agent-b's>      error loading snapshot …: unable to find manifest entries: access denied  exit=1
[agent-a] policy set … --keep-latest=1     can't save policy …: error writing policy manifest: access denied  exit=1
[agent-a] maintenance run --full           operation supported only on direct repository  exit=1
[maint]   snapshot list --all              (sees agent-a and agent-b)   exit=0
[maint]   snapshot delete <agent-a's> --delete                          exit=0
[maint]   maintenance run --full           operation supported only on direct repository  exit=1
```

With `--no-ui`, the REST endpoints return 404 for an agent user, and `/api/v1/control/*` returns 403. Only the gRPC session API is exposed to agents.

**The two gaps against ADR-0002's intent:**

1. **Content reads are not scoped per user.**
   - Any user with content READ can read *any* object whose ID it knows (`handleGetContentRequest` checks only `ContentAccessLevel()`). This READ comes from the default `*@*` APPEND on `type=content`, which must be kept because agents need to write content.
   - Object IDs are not enumerable, and agents cannot see other agents' manifests. But an object ID effectively works as a **bearer read capability**.
   - **Risk 1:** object IDs that reach an agent through logs, DBR² APIs or recovery manifests leak data.
   - **Risk 2:** the server hands the repository's **HMAC secret** to every client in the session handshake (`grpc_session.go:607`). So an agent can compute content IDs for plaintext it guesses, and the existence-check RPC then tells it whether that content exists in the repository. This is a cross-tenant confirmation-of-file oracle, and it is also why cross-agent dedup works.
2. **Server-side retention.**
   - `ApplyRetentionPolicy` is exposed over gRPC to anyone with APPEND on their own source. The server runs the deletions with **its own** privileges (`grpc_session.go:466-500`).
   - The stock CLI calls it after every `snapshot create`.
   - An agent cannot change the policy, but with Kopia's default global policy a compromised agent can prune its own history down to 10 latest, 48 hourly, 7 daily, and so on.
   - **Mitigation verified:** snapshots created with `Manifest.Pins = ["dbr2"]` are never pruned (`K/snapshot/policy/expire.go:77`). Removing a pin needs put plus delete, which requires FULL (maint only).
   - A belt-and-braces option is for maint to set a global retention policy with very large `keep-*` values. DBR² does its own retention (ADR-0004), so Kopia's should be inert.

**Other observations:**

- **ACL targets cannot use tags.** `type=snapshot` accepts only `hostname`, `username` and `path` labels. So there can be no per-RP grants, and a "time-limited read grant for one recovery point" (ADR-0002, cross-host restore) cannot be expressed. The closest options are:
  - a per-source (user@host[:path]) READ ACL, added and later deleted by DBR² itself, with no expiry in Kopia; or
  - staging the restore through maint; or
  - handing the target agent the root object ID. This works because content is global, and it is the capability model in practice.
- **An agent can write unlimited content** (APPEND on `type=content`). That is a storage-exhaustion DoS. Kopia has no per-user quota.
- **An agent can create snapshots with any tags on its own source,** including a fake `dbr2-kind=manifest`. DBR² must check that an RP manifest snapshot's `user@host` source matches the agent that owns the RP, and must cross-check against PostgreSQL.
- **ACL changes need a running server to refresh** (`kopia server refresh`, or the periodic reload). Users added while the server runs take effect in "5–10 minutes". The spike configured everything before starting the server.

### Q2d. Retention, deletion and GC in server mode

- **Only a direct connection can run maintenance.** `snapshotmaintenance.Run` takes a `repo.DirectRepositoryWriter`, and API clients get `operation supported only on direct repository`.
- **The server process runs maintenance itself.** `maybeStartMaintenanceManager` (`K/internal/server/server_maintenance.go:114`) starts only for a direct, writable connection. It runs `snapshotmaintenance.Run(..., ModeAuto, ..., SafetyFull)` (`K/internal/server/server.go:998`) when the server's identity is the repository's **maintenance owner**. That owner is `reposerver@dbr2` here, because it created the repository.
- **Schedule:** quick maintenance every 1 h, full maintenance every 24 h (`maintenance info` in `evidence/q2-cli.log`).
- **Safety delays with `SafetyFull`** (`K/repo/maintenance/maintenance_safety.go:58`):
  - content must be unreferenced for at least 24 h
  - two GC cycles are required
  - packs are deleted after at least 24 h
  - so space comes back roughly 1–2 days after a delete.
- **Verified flow:**
  1. `maint@dbr2` deleted agent-a's 50 MB snapshot through the API.
  2. On the server's direct connection, `maintenance run --full --safety=none` reported `GC found 13 unused contents (50.3 MB)`.
  3. Repository size on disk went from **52,634,005 to 8,630,502 bytes**. The remainder is the retention-test snapshots.
  4. `--safety=none` was used for the demo only.
- **Not observed within the time box:** GC by the server's own scheduler (it needs more than 24 h). That part is inferred from the code.
- **Can a client identity trigger deletion that GC later reclaims?** Yes, in two ways:
  - maint, with FULL on `type=snapshot`, deletes manifests directly;
  - any agent, through the server-side retention RPC (see gap 2).
  In both cases the space is reclaimed later, only by the server's own maintenance.

**Conclusion: Partial.** The manifest-level model works and is exactly expressible:

- agents can APPEND their own snapshots, cannot delete or modify them, and cannot see other agents' manifests;
- maint can list, restore, delete and set policies.

However:

- ADR-0002's "no access to other hosts' data" is **not** enforced at the data (content) level;
- "no delete" needs the pin or retention-policy mitigation;
- per-RP time-limited grants cannot be expressed;
- the server cannot be embedded as a Go API.

**Proposed ADR impact.**

- **ADR-0007:** Replace "`dbr2-reposerver`: the Kopia repository server" (embedded library) with: *`dbr2-reposerver` runs the pinned Kopia server by invoking Kopia's public `cli` package in-process (`server start …`), or, as a fallback, supervises the pinned upstream `kopia` binary. User and ACL management goes through the same CLI commands. The Kopia server, ACL and user packages are `internal/` and cannot be imported.* Both options were demonstrated.
- **ADR-0007 also:** "`dbr2-worker`: maintenance-identity operations" holds only for manifest operations (list, delete, policy, restore). Maintenance and GC must run inside `dbr2-reposerver`. It is the only holder of the repository password, and it must be the Kopia maintenance owner.
- **ADR-0002:**
  - Record the exact ACL set above. `server acl enable` defaults must be rewritten, not accepted.
  - Pin every component and manifest snapshot (`Pins=["dbr2"]`), and set a global Kopia retention policy that never expires anything. DBR² retention is done by maint.
  - Restate the consequence honestly: *a compromised host cannot list, delete or modify other hosts' snapshots, but it can read any content whose object ID it learns, and it can confirm whether guessed plaintext exists (the HMAC secret is shared with clients).* Therefore:
    - treat object IDs as secrets;
    - never send another host's object IDs to an agent;
    - for cross-host restore, prefer staging through the maint identity or a temporary per-source READ ACL that DBR² revokes (Kopia has no expiring grants).
  - Add storage quotas or monitoring per agent at the DBR² level.
  - Keep the reposerver's `--no-ui`.
  - Put S3 Object Lock or NAS snapshots underneath. This remains the only protection against a compromised reposerver.
- **ADR-0004:**
  - RP validation must check that the manifest snapshot's source user@host equals the RP's agent.
  - Orphan GC and RP deletion are maint operations. Actual space reclaim happens later through reposerver maintenance, about 24–48 h with `SafetyFull`.

---

## Q3: Data path in repository-server mode (ADR-0002)

**Question.** Where do splitting, hashing, compression and encryption happen? Does dedup avoid re-sending unchanged content?

### What the code says (v0.23.1)

| Step | Where | Evidence |
|---|---|---|
| Handshake | Server sends the client its hash function, **HMAC secret** and splitter | `K/internal/server/grpc_session.go:604-609` (`handleInitialSessionHandshake`); client side `K/repo/grpc_repository_client.go:1001-1012` |
| Splitting (content-defined chunking) | **Client** | `K/repo/object/object_writer.go:121` (`w.splitter.NextSplitPoint`), using the server-supplied splitter |
| Hashing / content ID | **Client** | `K/repo/grpc_repository_client.go:763` (`content.IDFromHash(prefix, r.h(...))`) |
| Dedup before send | **Client asks the server** | `doWriteAsync` (`:723-734`) calls `ContentInfo` (an existence RPC) for chunks of at least `writeContentCheckExistenceAboveSize = 50_000` bytes (`:42`) and skips the upload if the chunk exists. Smaller chunks are sent, and the server dedups them. There is also an in-memory `r.recent` cache (`:767`). |
| Compression | **Server** | If the repository supports content compression (format v2+, the default), the object writer does *not* compress. It passes a compression header ID down (`K/repo/object/object_writer.go:195-201`). The gRPC client sends raw data plus that ID (`WriteContentRequest{Data, Compression}`, `:780-790`). The server compresses in `maybeCompressAndEncryptDataForPacking` (`K/repo/content/content_manager_lock_free.go:30-70`). |
| Encryption | **Server** | Same function (AES256-GCM-HMAC-SHA256 here). The server's `WriteManager.WriteContent` (`K/repo/content/content_manager.go:791`) is reached from `handleWriteContentRequest` (`grpc_session.go:292`). Clients never hold the encryption key. On the wire the data is protected only by TLS. |

### Measured

**Method.**

- `cmd/q3` is a library client connected as `agent-a@hosta`.
- It connects to `https://127.0.0.1:51516`. An in-process byte-counting TCP proxy (`internal/proxy`, also available standalone as `cmd/proxy`) forwards to the upstream `kopia server` on `:51515`.
- TLS passes through, so the counts are real wire bytes.
- Each run opens a fresh client connection.
- The repository is BLAKE2B-256-128, AES256-GCM-HMAC-SHA256, `DYNAMIC-4M-BUZHASH`, format v3.
- The data is 20 × 10 MiB files (200 MiB).
- A "5 % change" overwrites 1 MiB in the middle of 10 of the files.

**Results with the default splitter** (`evidence/q3-bytes.log`):

| Run | client→server (wire) | server→client | Repository growth on server disk | Hashed by client |
|---|---:|---:|---:|---:|
| A1 initial full, 200 MiB (base64 random, incompressible) | **200.4 MiB** | 0.48 MiB | 200.0 MiB | 200.0 MiB |
| A2 unchanged, with previous manifest (hash cache) | **0.0 MiB** | 0.01 MiB | 0.0 MiB | 0.0 MiB |
| A3 unchanged, **no** previous manifest (full re-hash) | **0.0 MiB** | 0.01 MiB | 0.0 MiB | 200.0 MiB |
| A4 5 % changed | **58.3 MiB** | 0.14 MiB | 58.2 MiB | 100.0 MiB |
| B1 initial full, compressible log text, **zstd policy** | **200.4 MiB** | 0.46 MiB | **26.3 MiB** | 200.0 MiB |
| B2 5 % changed, zstd policy | **60.7 MiB** | 0.15 MiB | 16.5 MiB | 100.0 MiB |

**Same runs with `SPLITTER=DYNAMIC-1M-BUZHASH`** (`evidence/q3-bytes-1M-splitter.log`):

- A4 sent **36.0 MiB**
- B2 sent **35.9 MiB**; the repository grew by 13.1 MiB
- The other runs were essentially unchanged.

**Readings:**

- Unchanged data costs almost nothing on the wire, even without a previous manifest. This covers a new host, a new source path, or a lost hash cache: the client re-reads and re-hashes locally, then the existence check stops the upload. Dedup also works **across agents**, which is the same property that creates the existence oracle in Q2.
- A 5 % change costs about 29 % of the data set with the 4 MiB splitter. Each 1 MiB edit invalidates the surrounding 4 MiB-average chunk or chunks. With the 1 MiB splitter it costs about 18 %.
- **Compression policy does not reduce wire traffic.** B1 sent 200.4 MiB to store 26.3 MiB. This confirms the code reading.
- A first attempt used base64-of-random data with zstd. Kopia stored it uncompressed ("data was not compressible enough", `content_manager_lock_free.go:64`), and `kopia content stats` confirmed it. The compressible data set was switched to log-like text.

**Conclusion: Confirmed.** Splitting, hashing and the dedup decision happen on the client, so unchanged content is not re-sent. Compression and encryption happen on the server.

**Proposed ADR impact.**

- **ADR-0002:**
  - Replace the open item with: *in server mode, agents split, hash and dedup-check locally (unchanged data is ≈0 wire bytes), but compression and encryption happen in `dbr2-reposerver`. The agent→reposerver link carries **uncompressed** plaintext inside TLS.*
  - Consequences:
    1. WAN or remote agents are not helped by the Kopia compression policy. For database and image streams, the agent's own zstd pre-compression (ADR-0004 already specifies this for database dumps) is what saves WAN bandwidth, at some cost to dedup across dumps.
    2. reposerver CPU sizing must include compression and encryption for all agents.
    3. TLS on the agent link is mandatory; it is the only confidentiality layer there.
    4. Agents receive the repository HMAC secret (but not the encryption key).
  - Consider `DYNAMIC-1M-BUZHASH` for repositories with many large, slowly-changing files. It is chosen at repository creation and cannot be changed later.
- **Throughput with several agents** (the remaining ADR-0002 open item) was **not** measured in this spike.

---

## How to rerun

```bash
cd spikes/kopia-library
GOBIN=$PWD/bin go install github.com/kopia/kopia@v0.23.1   # pinned CLI/server into ./bin (gitignored)

# Q1 – library, filesystem repo (~2 s, ~700 MiB under .work/q1)
go run ./cmd/q1

# Q2 – repository server + ACLs (port 51515)
scripts/q2-setup.sh            # repo, users, DBR² ACL set
scripts/q2-server.sh           # upstream kopia server (TLS, self-signed)
go run ./cmd/q2                # library-client ACL matrix
scripts/q2-cli.sh              # stock CLI as client + delete + GC
go build -tags probe ./probes/internalimport   # expected to FAIL (internal import)
# same matrix against an in-process embedded server:
go build -o bin/embedded-kopia ./cmd/embedded-kopia
scripts/stop-server.sh && scripts/q2-setup.sh && KOPIA_BIN=$PWD/bin/embedded-kopia scripts/q2-server.sh && go run ./cmd/q2

# Q3 – wire bytes through proxy (51516 -> 51515)
scripts/stop-server.sh && scripts/q3-setup.sh && go run ./cmd/q3
scripts/stop-server.sh && SPLITTER=DYNAMIC-1M-BUZHASH scripts/q3-setup.sh && go run ./cmd/q3

scripts/stop-server.sh         # always
rm -rf .work                   # scratch data (up to ~1.7 GiB)
```

**Layout:**

- `internal/engine`: the only Kopia-importing package, and the reference for Phase 1.
- `internal/proxy`: the byte counter.
- `cmd/q1`, `cmd/q2`, `cmd/q3`: the harnesses.
- `cmd/embedded-kopia`: the in-process Kopia CLI and server.
- `cmd/proxy`: the standalone proxy.
- `scripts/`: server setup, start and stop.
- `evidence/`: logs quoted above.
