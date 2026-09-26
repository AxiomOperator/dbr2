// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { ContainersView } from "@/components/fleet/containers-view";

export const metadata: Metadata = { title: "Containers" };

export default function ContainersPage() {
  // The view keeps its filters in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense>
      <ContainersView />
    </Suspense>
  );
}
