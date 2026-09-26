// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { VolumesView } from "@/components/fleet/volumes-view";

export const metadata: Metadata = { title: "Volumes" };

export default function VolumesPage() {
  // The view keeps its filters in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense>
      <VolumesView />
    </Suspense>
  );
}
