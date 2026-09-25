// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { ApplicationDetailView } from "@/components/applications/application-detail";

export const metadata: Metadata = { title: "Application" };

export default async function ApplicationPage(props: PageProps<"/applications/[id]">) {
  const { id } = await props.params;
  return <ApplicationDetailView id={id} />;
}
