// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { UsageView } from "@/components/repositories/usage-view";

export const metadata: Metadata = { title: "Usage" };

export default function UsagePage() {
  return <UsageView />;
}
