// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { AboutView } from "@/components/about-view";

export const metadata: Metadata = { title: "About" };

/** Public (no session needed): /api/v1/version is public, useful when sign-in is broken. */
export default function AboutPage() {
  return <AboutView />;
}
