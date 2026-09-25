// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { RepositoryDetail } from "@/components/repositories/repository-detail";

export const metadata: Metadata = { title: "Repository" };

export default async function RepositoryPage(props: PageProps<"/repositories/[id]">) {
  const { id } = await props.params;
  return <RepositoryDetail id={id} />;
}
