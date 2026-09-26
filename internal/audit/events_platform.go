// SPDX-License-Identifier: Apache-2.0

package audit

// Phase 9: platform self-protection (ADR-0008). Stable event types (SIEM
// contract; see docs/dev/audit-events.md).
const (
	PlatformBackupRequested    = "platform.backup.requested"
	PlatformBackupSucceeded    = "platform.backup.succeeded"
	PlatformBackupPartial      = "platform.backup.partial"
	PlatformBackupFailed       = "platform.backup.failed"
	RepositorySystemDesignated = "repository.system.designated"
)
