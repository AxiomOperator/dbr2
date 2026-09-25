// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { HostsView } from "@/components/hosts/hosts-view";

export const metadata: Metadata = { title: "Hosts" };

export default function HostsPage() {
  return <HostsView />;
}
