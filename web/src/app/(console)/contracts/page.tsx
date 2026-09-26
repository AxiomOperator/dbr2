// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { ContractsView } from "@/components/policies/contracts-view";

export const metadata: Metadata = { title: "Recovery Contracts" };

export default function ContractsPage() {
  // The view keeps its state filter in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense>
      <ContractsView />
    </Suspense>
  );
}
