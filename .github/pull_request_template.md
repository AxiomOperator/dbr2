<!-- SPDX-License-Identifier: Apache-2.0 -->

## Summary

<!-- What changes and why. Link issues, ADRs and roadmap items. -->

## Checklist

- [ ] **Changelog (ADR-0015):** every component whose files changed has a new line under `## [Unreleased]` in its `CHANGELOG.md` (shared `internal/**` changes: each affected Go component — server, worker, agent, reposerver, cli). `api/openapi.yaml` changes need an `api/CHANGELOG.md` entry. Breaking API/protocol changes need a MAJOR bump in `api/VERSION` / `proto/agent/VERSION`. Test-, CI- or docs-only PRs may use the `no-changelog` label.
- [ ] **Roadmap:** `docs/roadmap.md` checklist items updated and a dated **Change Log** entry added **with notes** (what, why, files/ADRs/PRs) — see the mandatory update rule at the top of the roadmap.
- [ ] **DCO:** every commit is signed off by its author (`git commit -s`; fix existing commits with `git rebase --signoff <base>`) (ADR-0013).
- [ ] New source files carry `SPDX-License-Identifier: Apache-2.0`; dependency changes regenerate `THIRD_PARTY_NOTICES` (`scripts/ci/third-party-notices.sh`).
- [ ] Generated files are committed (`make generate`; CI runs `make check-generated`).

## Testing

<!-- How was this verified? Unit, integration, manual steps. -->
