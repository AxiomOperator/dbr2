// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { RestoreDetail } from "@/components/restores/restore-detail";

export const metadata: Metadata = { title: "Restore" };

export default async function RestorePage(props: PageProps<"/restores/[id]">) {
  const { id } = await props.params;
  return <RestoreDetail id={id} />;
}
