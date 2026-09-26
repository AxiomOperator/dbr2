// SPDX-License-Identifier: Apache-2.0

package audit

// Phase 7: notification channels, platform settings and agent presence
// (stable event types; see docs/dev/audit-events.md).
const (
	NotificationChannelCreated = "notification.channel.created"
	NotificationChannelUpdated = "notification.channel.updated"
	NotificationChannelDeleted = "notification.channel.deleted"
	NotificationChannelTested  = "notification.channel.tested"
	SMTPSettingsUpdated        = "settings.smtp.updated"
	AgentOffline               = "agent.offline"
	AgentOnline                = "agent.online"
)
