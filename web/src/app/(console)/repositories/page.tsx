// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { RepositoriesView } from "@/components/repositories/repositories-view";

export const metadata: Metadata = { title: "Repositories" };

export default function RepositoriesPage() {
  return <RepositoriesView />;
}
