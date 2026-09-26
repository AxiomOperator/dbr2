// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { PlatformView } from "@/components/platform/platform-view";

export const metadata: Metadata = { title: "Platform protection" };

export default function PlatformPage() {
  return <PlatformView />;
}
