// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { RecoveryPointsView } from "@/components/backups/recovery-points-view";

export const metadata: Metadata = { title: "Recovery points" };

export default function RecoveryPointsPage() {
  // The view reads ?application= and ?state= with useSearchParams, which needs a Suspense boundary.
  return (
    <Suspense>
      <RecoveryPointsView />
    </Suspense>
  );
}
