// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { AlertsView } from "@/components/backups/alerts";

export const metadata: Metadata = { title: "Alerts" };

export default function AlertsPage() {
  return <AlertsView />;
}
