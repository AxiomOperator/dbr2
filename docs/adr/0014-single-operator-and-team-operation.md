# ADR-0014: Operable by one person, ready for teams

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Today DBR² has one operator. As an open-source tool it must also serve teams. Several safeguards (the production-restore approval, dual authorization for destructive operations) assume a **second person**. In a single-operator deployment those would either block all work or be meaningless.

## Decision

1. **Everything works for one operator.** No feature requires a second person unless an administrator explicitly enables a multi-person control.
2. **Multi-person controls are optional, and validated when enabled.** Approval for production restores and dual authorization can be enabled only when at least **two eligible users** exist (users holding `restore.production` or the relevant permission, excluding the master admin). If that later stops being true (a user is removed or disabled), DBR² raises a warning. Requests are then automatically routed to the single-operator safeguards below instead of deadlocking.
3. **Single-operator safeguards (always on, v1.0):**
   - **Typed confirmation** for destructive operations and production restores (type the application or repository name).
   - A **deletion grace period** for manual deletions of recovery points and Repositories (default 7 days). Deleted items are recoverable until it expires.
   - A **mandatory reason** field for production restores and destructive operations. It is stored in the audit log.
   - An **impact preview** before every restore (see the roadmap).
4. **Team features stay in the model from day one:** users, roles, group-to-role mapping from Entra ID, per-user audit and delegated administration. So moving from one operator to a team is configuration, not a migration.
5. The **master admin** is excluded from approver counts, and it may perform an audited **emergency override** of any pending approval.

## Consequences

- The v1.0 roadmap includes the single-operator safeguards. The approval and dual-authorization workflows stay in v2.
- The threat model accepts that, in single-operator mode, one compromised operator account can issue destructive operations. This is mitigated by the grace period, NAS snapshots and ADR-0002 identities.
