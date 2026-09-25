// SPDX-License-Identifier: Apache-2.0
"use client";

import Link from "next/link";
import { ThemeToggle } from "@/components/theme-toggle";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { errorMessage } from "@/lib/api/client";
import { useVersion } from "@/lib/api/hooks";
import { consoleVersion, PRODUCT_NAME } from "@/lib/version";

export function AboutView() {
  const version = useVersion();
  const components = version.isSuccess
    ? Object.entries(version.data.components).sort(([a], [b]) => a.localeCompare(b))
    : [];

  return (
    <main className="mx-auto w-full max-w-3xl flex-1 space-y-6 px-4 py-8">
      <div className="flex items-center justify-between">
        <Link href="/" className="text-lg font-semibold tracking-tight">
          {PRODUCT_NAME}
        </Link>
        <ThemeToggle />
      </div>

      <div>
        <h1 className="text-2xl font-semibold tracking-tight">About {PRODUCT_NAME}</h1>
        <p className="text-sm text-muted-foreground">
          Docker Backup, Recovery &amp; Restore. Licensed under Apache-2.0; see{" "}
          <a
            href="https://github.com/AxiomOperator/dbr2"
            className="underline underline-offset-3 hover:text-foreground"
            target="_blank"
            rel="noopener noreferrer"
          >
            github.com/AxiomOperator/dbr2
          </a>
          .
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>
            <h2>Versions</h2>
          </CardTitle>
          <CardDescription>
            Every component is versioned <code>MAJOR.MINOR.BUGFIX.BUILD</code> (ADR-0015).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
            <dt className="text-muted-foreground">Platform</dt>
            <dd className="font-mono">
              {version.isSuccess ? version.data.platform : version.isPending ? "…" : "unavailable"}
            </dd>
            <dt className="text-muted-foreground">Console (this build)</dt>
            <dd className="font-mono">{consoleVersion()}</dd>
          </dl>

          {version.isPending && (
            <div className="space-y-2" aria-busy="true">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {version.isError && (
            <Alert variant="destructive">
              <AlertTitle>Component versions unavailable</AlertTitle>
              <AlertDescription>{errorMessage(version.error)}</AlertDescription>
            </Alert>
          )}
          {version.isSuccess && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">Component</TableHead>
                  <TableHead scope="col">Version</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {components.map(([name, v]) => (
                  <TableRow key={name}>
                    <TableCell>{name}</TableCell>
                    <TableCell className="font-mono">{v}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </main>
  );
}
