// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { RestoresView } from "@/components/restores/restores-view";

export const metadata: Metadata = { title: "Restores" };

export default function RestoresPage() {
  // The view reads ?application= and ?state= with useSearchParams, which needs a Suspense boundary.
  return (
    <Suspense>
      <RestoresView />
    </Suspense>
  );
}
