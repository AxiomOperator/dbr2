# CI and release scripts

<!-- SPDX-License-Identifier: Apache-2.0 -->

DBR² CI runs on GitHub Actions (`.github/workflows/`). Workflows stay thin: the
logic lives in these scripts so it can be run and tested locally. Every script
is bash (`set -euo pipefail`), carries an SPDX header, and runs from any
directory inside the repository.

Authoritative policy: ADR-0015 (versioning, changelogs, CI gates) and ADR-0013
(license, SPDX, THIRD_PARTY_NOTICES, DCO).

## Workflows

| Workflow | Trigger | What it does |
|---|---|---|
| `ci.yml` | push (any branch), pull_request | All gates below |
| `images.yml` | push to `main`; `workflow_call` from release | Build `dbr2-server`, `dbr2-worker`, `dbr2-reposerver` (`deployments/docker/Dockerfile.services`, `CMD`/`BUILD` args) and `dbr2-web` (`web/Dockerfile`); tag `ghcr.io/axiomoperator/<image>:<VERSION>.<BUILD>`, `:<VERSION>`, `:<MAJOR.MINOR>` (+ `:edge` on main); SPDX SBOM per image; push to GHCR; cosign keyless sign + SBOM attestation |
| `release.yml` | manual (`workflow_dispatch`, main only, `dry_run` input) | Finalize changelogs, write `release-manifest.json`, signed-off release commit, tags, binaries + SBOMs + checksums (cosign-signed), GitHub Release, images |

All third-party actions are pinned to full commit SHAs with a `# vX.Y.Z`
comment. Dependency and action updates are applied manually (Dependabot version
updates were removed at the owner's request; see docs/roadmap.md). Every
job has least-privilege `permissions` (default `contents: read`).

## CI gates (`ci.yml` jobs)

| Job | Enforces | Script / command |
|---|---|---|
| `go` | `go vet`, gofmt, SPDX headers; unit tests (`-race`); build with `BUILD=run_number`; generated code committed and current | `make lint test build check-generated` |
| `integration` | integration tests (`-tags integration`, testcontainers, Docker) | `make test-integration` |
| `web` | lint, typecheck, unit tests (incl. generated-contract staleness), production build | `make web-install web-test web-build` |
| `web-e2e` | Playwright end-to-end tests (Chromium) against the mock API and a production build; fails on any CSP violation; report uploaded on failure | `make web-e2e` |
| `api-contract` | `api/openapi.yaml` is committed and matches `make openapi`; on PRs, no breaking change without an `api` version bump (see *Breaking-change policy*); oasdiff changelog in the job summary | `check-api-contract.sh` |
| `proto` | `buf lint`; on PRs, `buf breaking` against the base branch without an `agent-protocol` version bump fails (see *Breaking-change policy*; skips with a notice if `buf.yaml` is absent) | `check-proto.sh` |
| `changelog` (PR) | component changelog entries (ADR-0015) | `check-changelog.sh` |
| `dco` (PR) | `Signed-off-by` on every commit | `check-dco.sh` |
| `license` | SPDX headers; `THIRD_PARTY_NOTICES` current; dependency license allow-list | `check-spdx.sh`, `third-party-notices.sh --check` |
| `security` | `govulncheck` (pinned), `npm audit --omit=dev --audit-level=high`, gitleaks secret scan of the full history | inline |
| `actionlint` | workflow syntax, expressions and embedded shell (shellcheck) | `install-tool.sh actionlint` |
| `ci-scripts` | shellcheck of these scripts and their test suite | `test/run.sh` |

## Scripts

### `check-changelog.sh <base-sha> <head-sha>`

For every component whose files changed between `merge-base(base, head)` and
`head`, that component's `CHANGELOG.md` must gain at least one **entry line**
(non-blank, not a heading) inside `## [Unreleased]`. Component paths come from
`lib/components.sh`:

`api/` · `cmd/server/` · `cmd/worker/` · `cmd/agent/` · `cmd/reposerver/` ·
`cmd/dbr2/` (cli) · `web/` · `proto/agent/` (agent-protocol) ·
`internal/manifest/` (manifest-schema) · `db/` (db-schema) · `deployments/`

- `api/openapi.yaml` is inside `api/`, so spec changes need an `api` entry.
- **Shared Go code** — `internal/**` outside `internal/manifest/`, plus
  `go.mod`/`go.sum` — affects server, worker, agent, reposerver and cli. The
  heuristic: at least one of those five changelogs must gain an entry (record
  it in each one the change really affects). The failure message lists them.
- Editing only a component's `CHANGELOG.md` requires nothing more.
- **`no-changelog` label**: passes the check only when *every* changed file is
  test, CI or docs: `docs/**`, `.github/**`, `scripts/ci/**`, `**/*_test.go`,
  `**/testdata/**`, `tests/**`, `spikes/**`, or `*.md` outside component
  directories. Otherwise the label is ignored and the normal check runs.
  Labels are read live (`$PR_LABELS`, fetched by the job with `gh api`), so
  after adding the label **re-run the `changelog` job**.

### `check-dco.sh <base-sha> <head-sha>`

Every non-merge commit in `base..head` needs a `Signed-off-by:` trailer whose
email matches the author email (case-insensitive) — `git commit -s`; fix a
branch with `git rebase --signoff <base>`. Exemptions:

- `.github/dco-exempt-commits`: full SHAs of pre-policy commits. The list is
  read from the **base** commit so a PR cannot exempt itself (the head copy is
  used only when the base has none, i.e. the PR that introduces it).
- Dependabot commits (`49699333+dependabot[bot]@users.noreply.github.com`).
- Merge commits.

### `check-spdx.sh`

Every tracked or untracked-but-not-ignored `*.go`, `*.sql`, `*.proto`, `*.sh`,
`Dockerfile*`, `web/src/**/*.{ts,tsx,js,mjs}`, `web/scripts/**` and
`.github/workflows/*.y{a,}ml` must contain `SPDX-License-Identifier: Apache-2.0`
in its first 5 lines. Skipped: `spikes/**`, `node_modules/`, lockfiles,
`*.sum`, and generated files whose first 5 lines match
`^// Code generated .* DO NOT EDIT\.$` (sqlc, protoc-gen-go). Run by `make lint`.

### `third-party-notices.sh [--check] [--out FILE]`

Regenerates `THIRD_PARTY_NOTICES` (repository root) from:

- Go production dependencies of `./...` (linux/amd64, excluding
  `web/node_modules`), via pinned `go-licenses/v2 report` with a template;
- web production dependencies from `web/package-lock.json` (entries not
  marked `dev`/`devOptional`; platform-specific optional packages are listed
  for every platform so the file is machine-independent). License and NOTICE
  texts come from `web/node_modules` — run `make web-install` first.

Distinct license/NOTICE texts are printed once with the packages they cover.
`--check` fails when the committed file differs from a fresh render.

License policy (both ecosystems; SPDX `OR`/`AND` expressions evaluated):
allowed Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, ISC, 0BSD, Unlicense,
CC0-1.0, Zlib, BlueOak-1.0.0, CC-BY-4.0; MPL-2.0 allowed with a warning;
GPL/AGPL/LGPL/SSPL/BUSL denied; anything else (including `Unknown`) fails.
Reviewed exceptions live in `license-exceptions` (`<go|npm>:<name glob>` with a
justification) and are always reported as warnings. Current exceptions: the
LGPL-3.0-or-later libvips binaries in Next.js's optional `sharp` packages
(`@img/sharp-libvips-*`, `@img/sharp-wasm32`, `@img/sharp-win32-*`; approved by
the session lead, pending owner review) and
`nexus-rpc/nexus-proto-annotations` (MIT upstream, no LICENSE file in v0.1.0).

### `check-api-contract.sh <base-ref>`

Compares `<base-ref>:api/openapi.yaml` with the working tree using oasdiff
(pinned by `OASDIFF_VERSION` in the Makefile):
`oasdiff breaking --fail-on ERR -f githubactions` fails unless `api/VERSION`
was bumped per the breaking-change policy; `oasdiff changelog -f markdown`
goes to the job summary. Skips when the base has no spec.

### `check-proto.sh [<base-ref>]`

`buf lint`; with a base ref, `buf breaking --against .git#ref=<base sha>`,
allowed only when `proto/agent/VERSION` was bumped per the breaking-change
policy. Skips with a notice when `buf.yaml` is missing (here or in the base).

### Breaking-change policy (`lib/semver.sh`)

Shared by the API and protocol checks, comparing the base and head `VERSION`:

| Base version | Breaking change allowed when |
|---|---|
| `0.x.y` (pre-1.0) | MINOR or MAJOR increased (semver 0.x convention), e.g. `0.1.0` → `0.2.0` |
| `1.x.y` and later | MAJOR increased, e.g. `1.4.0` → `2.0.0` |

A BUGFIX-only bump never allows a breaking change.

### `install-tool.sh <tool>`

Installs a pinned tool into `./bin` with `go install` and prints its path.
Versions of oasdiff, actionlint, govulncheck and buf are read from the
Makefile (shared with `make tools`); go-licenses (v2.0.1) and gitleaks
(v8.30.1) are pinned in the script.

### `lib/components.sh`

The ADR-0015 component map (name, path, contract/built) and helpers
(`component_for_path`, `full_version`). Contract components (`api`,
`agent-protocol`, `manifest-schema`, `db-schema`) always use BUILD 0.

### `../release/finalize-changelogs.sh --build N [--date D] [--root DIR]`

For each changelog with entries under `[Unreleased]`, moves them to
`## [<VERSION>.<N>] - <D>` (BUILD 0 for contract components), dropping empty
subsections, and leaves a fresh empty `[Unreleased]` template. The root
`CHANGELOG.md` gets a platform section listing the released components.
Prints `<name> <version>` per finalized changelog. Idempotent: an empty
`[Unreleased]` is left byte-identical; entries whose target version already
exists are an error (bump `VERSION`).

### `../release/release-manifest.sh --build N [--changed FILE] [--base-commit SHA]`

Prints `release-manifest.json`: platform version and tag, and for every
component its version, path, contract flag, tag and whether it was released.

### Other files

- `.github/dco-exempt-commits` — DCO exemptions (see above).
- `.github/.gitleaksignore` — reviewed gitleaks false positives (fingerprints).
- `license-exceptions` — reviewed license-policy exceptions.

## Tests

```sh
scripts/ci/test/run.sh
```

Builds throwaway git repositories in a temp directory and covers
`check-changelog.sh` (pass, fail, label bypass, shared code, merge-base),
`check-dco.sh` (signed, unsigned, wrong signer, exempt, self-exemption, merges,
bots), `finalize-changelogs.sh` / `release-manifest.sh` (moves entries,
idempotent, empty `[Unreleased]` untouched, contract BUILD 0), the
breaking-change version policy (`lib/semver.sh`) and `check-spdx.sh`. Run `shellcheck -x scripts/ci/*.sh scripts/ci/lib/*.sh
scripts/ci/test/*.sh scripts/release/*.sh` and `./bin/actionlint` before
pushing workflow changes.

## Release notes for maintainers

- The release job pushes the release commit and tags to `main` with
  `GITHUB_TOKEN`. If `main` is protected, allow GitHub Actions to bypass the
  rule (or use a deploy key / GitHub App token).
- Pushes made with `GITHUB_TOKEN` do not trigger other workflows, so the
  release commit does not re-run CI or `images.yml`; release calls
  `images.yml` itself.
- Use `dry_run: true` first: the would-be commit's changelogs, the manifest,
  binaries and SBOMs are uploaded as the `release-dry-run` artifact.
