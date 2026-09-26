// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { AgentsView } from "@/components/hosts/agents-view";

export const metadata: Metadata = { title: "Agents" };

export default function AgentsPage() {
  return <AgentsView />;
}
