# DBR² — Docker Backup, Recovery & Restore

DBR² protects **Docker applications**, not just volumes. It discovers Compose stacks, captures their definition, persistent data and databases as atomic, verifiable recovery points, and restores them onto the same or another Docker host.

> **Status: Phases 1–6 complete.** Foundations (control plane, authentication, RBAC, audit, console, CI), agents (enrollment with single-use tokens and CA pinning, approval, mTLS gateway, durable command journal, RPM packages for Rocky and Fedora), discovery (Compose applications, volume classification, external dependencies, unprotected-data detection, reconstructed Compose, secret masking) and **backup**: Repositories on an embedded Kopia server with mandatory age key escrow; Live, Quiesced and Offline application backups with hooks, a saga plus an agent dead-man switch; recovery manifests committed last by a maintenance identity; reindex; and orphan GC. **Restore** (in place or to another host) comes with an impact preview and collision detection, production safeguards, a staged and verified swap with automatic rollback, re-creation of missing containers, and a recovery history. The **web console** has live updates (Server-Sent Events), protection status and coverage, topology graphs, a nonce-based CSP and Playwright end-to-end tests. Next: scheduling, retention and notifications (see [`docs/roadmap.md`](docs/roadmap.md)).

## Documentation

- [`docs/stack_info/final_stack.md`](docs/stack_info/final_stack.md): architecture and technology stack (the source of truth)
- [`docs/adr/`](docs/adr/README.md): architecture decision records
- [`docs/roadmap.md`](docs/roadmap.md): phases, checklists and change log
- [`docs/domain_model.md`](docs/domain_model.md), [`docs/threat_model.md`](docs/threat_model.md)
- [`deployments/docker-compose/README.md`](deployments/docker-compose/README.md): install and operations

## Development

```bash
make build            # Go binaries → ./bin (BUILD=<n> sets the build number)
make test             # unit tests, including the OpenAPI spec lint and the Temporal usage lint
make test-integration # PostgreSQL 18 and Temporal through Docker (testcontainers)
make generate         # versions, sqlc, protobuf, OpenAPI (commit the results)
make dev-up           # full stack at https://localhost:9443 with a mocked NFS Repository
tests/nfs/run.sh      # functional NFS storage-guard test (privileged Docker)
```

Components are versioned independently as `MAJOR.MINOR.BUGFIX.BUILD`, and each keeps its own `CHANGELOG.md` (ADR-0015). Every change also updates `docs/roadmap.md` with notes. Commits require a DCO sign-off (`git commit -s`).

## License

Apache License 2.0; see [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE). Derivative works must retain the `NOTICE`, which credits the original project at https://github.com/AxiomOperator/dbr2. Third-party notices: [`THIRD_PARTY_NOTICES`](THIRD_PARTY_NOTICES).
