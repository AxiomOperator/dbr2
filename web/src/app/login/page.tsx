// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { Suspense } from "react";
import { LoginView } from "@/components/login/login-view";

export const metadata: Metadata = { title: "Sign in" };

export default function LoginPage() {
  // useSearchParams() in LoginView needs a Suspense boundary for prerendering.
  return (
    <Suspense>
      <LoginView />
    </Suspense>
  );
}
