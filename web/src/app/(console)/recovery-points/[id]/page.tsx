// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { RecoveryPointDetail } from "@/components/backups/recovery-point-detail";

export const metadata: Metadata = { title: "Recovery point" };

export default async function RecoveryPointPage(props: PageProps<"/recovery-points/[id]">) {
  const { id } = await props.params;
  return <RecoveryPointDetail id={id} />;
}
