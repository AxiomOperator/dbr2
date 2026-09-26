// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { PolicyDetail } from "@/components/policies/policy-detail";

export const metadata: Metadata = { title: "Policy" };

export default async function PolicyPage(props: PageProps<"/policies/[id]">) {
  const { id } = await props.params;
  return <PolicyDetail id={id} />;
}
