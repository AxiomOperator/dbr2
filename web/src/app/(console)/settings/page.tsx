// SPDX-License-Identifier: Apache-2.0
import { redirect } from "next/navigation";

export default function SettingsIndexPage() {
  redirect("/settings/security");
}
