# Changelog — agent-protocol

All notable changes to the `agent-protocol` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Additive (Phase 8): `ComponentSpec.database` (`DatabaseSpec`), `ComponentResult.database` and `validation`. Control: retention (`ListRetentionCandidates`, `Begin/FinishRecoveryPointDeletion`), verification (`ListVerificationCandidates`, `SetVerification`) and `PrepareBackupResponse.contract_json`.
- Additive (Phase 9, control plane `control.v1.PlatformService`): `BeginPlatformBackup`, `ExportPlatform` (server-streaming `ExportPlatformResponse` chunks with a final `ExportPlatformSummary`) and `RecordPlatformBackup` for the Platform Protection workflow (ADR-0008).
- Additive (Phase 5): `EnsureImages`, `RestoreComponents` (`RestoreSpec`, `PathRemap`), `RecreateContainers` (`NetworkSpec`), `StartContainers`, `CheckHealth`, `FinalizeRestore` (`FinalizeAction`), `RestoreDatabase` and their results; `ComponentResult.file_name`. Control: `PrepareRestore`, `GrantRestoreAccess`, `RevokeRestoreAccess`, `UpdateRestore`.
- Additive (non-breaking, Phase 4): commands `ConfigureRepository`, `Quiesce` (dead-man lease), `Resume`, `RunHooks`, `SnapshotComponents` (with `ComponentSpec`, seed flag) and their results; `AgentEvent` (agent → gateway); `Welcome.max_concurrent_jobs`. Internal `control.v1.PlatformService` for dbr2-worker.
- Additive (non-breaking): `Command` (discover, echo), `CommandUpdate` with `CommandState`, `CommandAck`, `Reject`, `InventoryReport`, `HealthReport`, heartbeat `echo_unix_ms`; `EnrollmentService` (`GetCA`, `Enroll`, `Renew`). Internal `control.v1.GatewayControlService.Dispatch` for dbr2-worker.
- Component scaffold (Phase 1).
- `proto/agent/v1/agent.proto`: `AgentService.Connect` bidirectional stream with the `Hello` handshake (agent and protocol versions, host facts, in-flight command IDs), `Welcome` and `Heartbeat`. Buf STANDARD lint, FILE breaking rules; Go code generated into `internal/agentpb`.
