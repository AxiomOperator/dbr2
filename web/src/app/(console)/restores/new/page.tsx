// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { StartRestoreView } from "@/components/restores/start-restore";

export const metadata: Metadata = { title: "Start a restore" };

export default function StartRestorePage() {
  // The view keeps its filters in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense>
      <StartRestoreView />
    </Suspense>
  );
}
