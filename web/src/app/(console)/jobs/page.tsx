// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { JobsView } from "@/components/jobs/jobs-view";

export const metadata: Metadata = { title: "Jobs" };

export default function JobsPage() {
  // The view keeps its filters in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense>
      <JobsView />
    </Suspense>
  );
}
