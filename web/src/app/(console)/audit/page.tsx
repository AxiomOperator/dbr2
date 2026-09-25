// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { AuditLog } from "@/components/audit/audit-log";

export const metadata: Metadata = { title: "Audit log" };

export default function AuditPage() {
  return <AuditLog />;
}
