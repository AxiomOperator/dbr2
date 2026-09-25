# Changelog — agent-protocol

All notable changes to the `agent-protocol` component. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are `MAJOR.MINOR.BUGFIX.BUILD` (ADR-0015).

## [Unreleased]

### Added
- Component scaffold (Phase 1).
- `proto/agent/v1/agent.proto`: `AgentService.Connect` bidirectional stream with the `Hello` handshake (agent and protocol versions, host facts, in-flight command IDs), `Welcome` and `Heartbeat`. Buf STANDARD lint, FILE breaking rules; Go code generated into `internal/agentpb`.
