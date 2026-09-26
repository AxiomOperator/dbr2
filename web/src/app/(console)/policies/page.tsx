// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { PoliciesView } from "@/components/policies/policies-view";

export const metadata: Metadata = { title: "Policies" };

export default function PoliciesPage() {
  return <PoliciesView />;
}
