// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { HostDetail } from "@/components/hosts/host-detail";

export const metadata: Metadata = { title: "Host" };

export default async function HostPage(props: PageProps<"/hosts/[id]">) {
  const { id } = await props.params;
  return <HostDetail id={id} />;
}
