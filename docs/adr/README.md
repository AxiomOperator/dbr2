# Architecture Decision Records

Each ADR records one significant decision: its context, the decision, and its consequences. `../stack_info/final_stack.md` must always reflect the **Accepted** ADRs. When an ADR changes status, update final_stack and add an entry to `../roadmap.md`.

**Statuses:** Proposed → Accepted → (Superseded by ADR-XXXX | Deprecated)

| ADR | Title | Status |
|---|---|---|
| [0001](0001-worker-agent-execution-model.md) | Worker–agent execution model (Agent Gateway) | Accepted |
| [0002](0002-kopia-repository-server.md) | Repository access through a Kopia Repository Server | Accepted |
| [0003](0003-repository-is-authoritative-for-recovery-data.md) | Repository is authoritative for recovery data; PostgreSQL is a rebuildable index | Accepted |
| [0004](0004-recovery-point-model.md) | Recovery point structure and atomic commit | Accepted |
| [0005](0005-quiesce-safety.md) | Quiesce safety: saga compensation, timeouts and agent dead-man switch | Accepted |
| [0006](0006-native-agent-and-volume-access.md) | Native agent and host-level volume access | Accepted |
| [0007](0007-kopia-embedded-library.md) | Kopia integrated as an embedded Go library | Accepted |
| [0008](0008-platform-self-backup-and-key-escrow.md) | Platform self-backup and key escrow | Accepted |
| [0009](0009-temporal-orchestration.md) | Temporal for durable orchestration | Accepted |
| [0010](0010-valkey-replaces-dragonfly.md) | Valkey replaces Dragonfly | Accepted |
| [0011](0011-concurrency-control.md) | Concurrency control without cache locks | Accepted |
| [0012](0012-terminology.md) | Terminology: disambiguating "repository" | Accepted |
| [0013](0013-open-source-license.md) | Open-source license: Apache-2.0 with NOTICE attribution | Accepted |
| [0014](0014-single-operator-and-team-operation.md) | Operable by one person, ready for teams | Accepted |
| [0015](0015-component-versioning-and-changelogs.md) | Per-component versioning (`MAJOR.MINOR.BUGFIX.BUILD`) and changelogs | Accepted |
| [0016](0016-agent-enrollment-and-pki.md) | Agent enrollment, PKI and the gateway endpoint | Accepted |
| [0017](0017-restore-staging-swap-and-rollback.md) | Restore: staging, swap and rollback | Accepted |

## Template

```markdown
# ADR-NNNN: Title

- **Status:** Proposed | Accepted | Superseded by ADR-XXXX
- **Date:** YYYY-MM-DD

## Context
## Decision
## Consequences
## Open items / validation
```
