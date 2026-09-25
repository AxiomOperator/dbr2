# NAS guidance for the Repository share (v1.0)

<!-- SPDX-License-Identifier: Apache-2.0 -->

In v1.0 a Repository lives on a single NAS, exported over NFS and mounted **only** on the `dbr2-reposerver` host (final_stack → Storage targets). S3 Object Lock is not available on NFS, so **scheduled read-only NAS snapshots of the Repository share are the v1.0 substitute for immutability**. They protect against:

- ransomware or a compromised control plane deleting or encrypting Repository files through the mount;
- an operator mistake (deleting the share contents, pointing a second Repository at the same path);
- Kopia or DBR² bugs that damage repository blobs.

They do **not** replace the off-NAS copies: the Platform Recovery Bundle (ADR-0008) and Veeam's VM-level backup of the DBR² server.

## Export

| Setting | Value | Why |
|---|---|---|
| Protocol | NFSv4.1 or v4.2 | The mount guard requires `nfs4` |
| Clients | the reposerver host's address **only** | Agents never mount the share (ADR-0002) |
| Squash | `root_squash`; files owned by the reposerver's uid (65532 in the container image) | The reposerver never needs root on the share |
| Access | read-write for that client; no other clients | |
| Snapshot directory | hidden or not exported (`.snapshot` invisible to the client) | Snapshots must not be deletable through the mount |

## Mount on the reposerver host

```sh
# /etc/fstab
nas.example.lan:/dbr2-repo  /mnt/dbr2-repo  nfs4  hard,timeo=600,retrans=2,noatime,_netdev  0 0
```

- **`hard`, never `soft`:** a `soft` mount returns I/O errors in the middle of writes and can corrupt the repository. A `hard` mount blocks during an outage, the reposerver's stall watchdog reports it, and writes resume afterwards (spike-verified).
- The reposerver refuses to run unless the path is an active `nfs4` mount containing the matching `.dbr2-repository-id` sentinel. This prevents silently writing a repository onto the local disk.

## Snapshot schedule

Configure these on the NAS, not on DBR²:

| Schedule | Keep | Notes |
|---|---|---|
| Every 4 hours | 2 days | Short-term rollback after a bad maintenance run |
| Daily | 14 days | Must exceed the orphan-GC grace (7 days) plus Kopia's deletion delay (about 24–48 h) |
| Weekly | 8 weeks | |
| Monthly | 6 months | Adjust to your longest retention requirement |

- Snapshots must be **read-only**, and deleting them must require a separate NAS administrator account that DBR² administrators do not use day to day.
- Enable snapshot locking or retention locking if the NAS supports it.
- Watch NAS free space: snapshots of a deduplicated Kopia repository grow with churn (new and deleted blobs), not with the size of the protected data.

## Restoring from a NAS snapshot

A NAS snapshot is a crash-consistent copy of the whole repository, so restore it as a whole:

1. Stop `dbr2-reposerver`.
2. Restore the share (or clone the snapshot to a new share) from the NAS.
3. Start `dbr2-reposerver`; it checks the sentinel and serves the restored repository.
4. Run `dbr2 admin reindex --repository <name>`. Recovery points newer than the snapshot are marked **missing** and raise an alert; everything in the snapshot is indexed again (ADR-0003).

If the reposerver state was also lost, create the Repository connection again with the escrowed password. The management API's `initialize` reconnects to an existing repository instead of creating a new one.

## Verification

- Quarterly: clone a NAS snapshot to a scratch share and connect with a stock `kopia` CLI using the escrowed password (`kopia repository connect filesystem --path … --readonly`, then `kopia snapshot list --all --tags dbr2-kind:manifest`).
- After NAS firmware upgrades: confirm the snapshot schedule and locking are still active.
