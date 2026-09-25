// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { ApplicationsView } from "@/components/applications/applications-view";

export const metadata: Metadata = { title: "Applications" };

export default function ApplicationsPage() {
  // The view reads ?host= with useSearchParams, which needs a Suspense boundary.
  return (
    <Suspense>
      <ApplicationsView />
    </Suspense>
  );
}
