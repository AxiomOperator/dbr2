// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { NotificationsView } from "@/components/notifications/notifications-view";

export const metadata: Metadata = { title: "Notifications" };

export default function NotificationsPage() {
  // The view keeps the selected tab in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense>
      <NotificationsView />
    </Suspense>
  );
}
