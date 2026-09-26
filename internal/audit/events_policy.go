// SPDX-License-Identifier: Apache-2.0

package audit

// Phase 7 (policies, retention, contracts) and Phase 9 (verification,
// escrow health) event types.
const (
	PolicyCreated                = "policy.created"
	PolicyUpdated                = "policy.updated"
	PolicyDeleted                = "policy.deleted"
	ApplicationPolicyAssigned    = "application.policy.assigned"
	ContractUpdated              = "contract.updated"
	ContractViolated             = "contract.violated"
	ContractSatisfied            = "contract.satisfied"
	BackupSkipped                = "backup.skipped"
	RecoveryPointDeleteScheduled = "recovery_point.delete.scheduled"
	RecoveryPointDeleteCanceled  = "recovery_point.delete.canceled"
	RecoveryPointDeleted         = "recovery_point.deleted"
	RepositoryDeleteScheduled    = "repository.delete.scheduled"
	RepositoryDeleteCanceled     = "repository.delete.canceled"
	RepositoryRetired            = "repository.retired"
	VerificationRequested        = "verification.requested"
	VerificationSucceeded        = "verification.succeeded"
	VerificationFailed           = "verification.failed"
	EscrowUnhealthy              = "escrow.unhealthy"
	EscrowRegenerated            = "repository.escrow.regenerated"
	EscrowDrillStarted           = "escrow.drill.started"
	EscrowDrillCompleted         = "escrow.drill.completed"
)
