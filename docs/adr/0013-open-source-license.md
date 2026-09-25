# ADR-0013: Open-source license: Apache-2.0 with NOTICE attribution

- **Status:** Accepted (owner approved 2026-09-25)
- **Date:** 2026-09-25

## Context

DBR² starts as an internal tool and will be published as open source. The owner's terms:

- free to use, modify and commercialize
- **on the condition that the original repository is credited ("memorialized")**

## Options

| License | Commercial use | Attribution strength | Notes |
|---|---|---|---|
| **Apache-2.0** | Yes | Copies must keep the license and the **`NOTICE` file**. The `NOTICE` can name the original project and its repository, and redistributors must carry it forward. | Includes an explicit patent grant. Same license as Kopia. |
| MIT | Yes | Only the copyright and permission notice must be kept. There is no mechanism to require crediting the original repository. | Simplest, but the weakest fit for "credit the original repo". |
| BSD-3-Clause | Yes | Like MIT, plus a clause forbidding use of the author's name for endorsement | Same attribution gap as MIT |
| GPL/AGPL | Yes, but derivatives must stay open | Strong, through copyleft | Conflicts with "free to commercialize" as most people understand it |

## Decision

**Apache License 2.0**, with a `NOTICE` file at the root of the codebase:

```text
DBR² — Docker Backup, Recovery & Restore
Copyright 2026 DBR2 Team
Original project: https://github.com/AxiomOperator/dbr2

This product includes software developed as part of the DBR² project.
Derivative works must retain this NOTICE, including the original repository reference.
```

- Every source file carries an SPDX header: `// SPDX-License-Identifier: Apache-2.0`.
- Third-party notices (Kopia, Temporal SDK, Valkey client and others) are collected in `THIRD_PARTY_NOTICES` and kept current as part of the release checklist.
- Inbound contributions come under the same license: Apache-2.0 §5, plus DCO sign-off (`Signed-off-by`).

## Consequences

- Anyone may fork, modify and sell DBR², but distributions must keep the `NOTICE` that credits the original repository.
- Apache-2.0 cannot force a *visible in-product* credit. If that is required, add a "Powered by DBR²" attribution requirement; it would need a custom addition, which makes the license non-standard. That is not recommended.

## Implementation

- `LICENSE` (the canonical Apache-2.0 text) and `NOTICE` were added at the root of the codebase on 2026-09-25.
- Copyright holder: **DBR2 Team**. Canonical repository: **https://github.com/AxiomOperator/dbr2**.
