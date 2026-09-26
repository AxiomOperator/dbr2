// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { RestoreWizard } from "@/components/restores/restore-wizard";

export const metadata: Metadata = { title: "Restore recovery point" };

export default async function RestoreRecoveryPointPage(props: PageProps<"/recovery-points/[id]/restore">) {
  const { id } = await props.params;
  return <RestoreWizard recoveryPointId={id} />;
}
