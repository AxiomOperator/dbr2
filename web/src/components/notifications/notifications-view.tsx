// SPDX-License-Identifier: Apache-2.0
"use client";

import { AlertsPanel } from "@/components/backups/alerts";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { useUrlFilters } from "@/components/common/filters";
import { ChannelsPanel } from "@/components/notifications/channels";
import { SmtpSettingsPanel } from "@/components/notifications/smtp-settings";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { PERMISSION_BACKUP_READ, PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";

export const NOTIFICATION_TABS = ["alerts", "channels", "smtp"] as const;
export type NotificationTab = (typeof NOTIFICATION_TABS)[number];

/** System → Notifications: Alerts | Channels | SMTP (the tab is kept in the URL, `?tab=`). */
export function NotificationsView() {
  const me = useCurrentUser();
  const canAlerts = hasPermission(me, PERMISSION_BACKUP_READ);
  const canPolicy = hasPermission(me, PERMISSION_POLICY_READ);
  const available = NOTIFICATION_TABS.filter((t) => (t === "alerts" ? canAlerts : canPolicy));
  const first = available[0] ?? "alerts";
  const [params, setParams] = useUrlFilters({ tab: first }, { tab: available });
  const tab = params.tab as NotificationTab;
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Notifications</h1>
        <p className="text-sm text-muted-foreground">Alerts raised by DBR², and where they are delivered.</p>
      </div>
      <Tabs value={tab} onValueChange={(v) => setParams({ tab: v })}>
        <TabsList>
          {canAlerts && <TabsTrigger value="alerts">Alerts</TabsTrigger>}
          {canPolicy && <TabsTrigger value="channels">Channels</TabsTrigger>}
          {canPolicy && <TabsTrigger value="smtp">SMTP</TabsTrigger>}
        </TabsList>
        <TabsContent value="alerts" className="mt-4">
          <AlertsPanel />
        </TabsContent>
        <TabsContent value="channels" className="mt-4">
          <ChannelsPanel />
        </TabsContent>
        <TabsContent value="smtp" className="mt-4">
          <SmtpSettingsPanel />
        </TabsContent>
      </Tabs>
    </div>
  );
}
