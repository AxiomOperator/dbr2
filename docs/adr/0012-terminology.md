# ADR-0012: Terminology: disambiguating "repository"

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

"Repository" was used for four different things: the user-facing backup store, the Go abstraction over Kopia, sqlc-style data access, and source-code repositories. This causes confusion in docs, code and conversations.

## Decision

| Concept | Term | Examples |
|---|---|---|
| A user-configured, Kopia-backed store of recovery points | **Repository** (always this meaning) | "Repository: Primary-NAS", `repository.manage`, `dbr2-reposerver` |
| The physical target behind a Repository | **Storage backend** | Local filesystem, NFS, SMB, S3-compatible |
| The Go abstraction over Kopia | **Backup engine** (`BackupEngine` interface, `internal/engine/`) | `engine/kopia` |
| Go data-access layer over PostgreSQL (sqlc) | **Store** (`internal/store/`) | `store.Queries`; never call it the "repository pattern" |
| A source-code repository | **Source repository** (or "Git repository"), always qualified | Source-repository linkage |
| The DBR² codebase itself | **Codebase** / **monorepo** | — |

All docs are updated to use these terms. The glossary lives in `../domain_model.md`.
