// SPDX-License-Identifier: Apache-2.0
"use client";

import { ArrowLeftIcon } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ReactNode } from "react";
import {
  BindMountsSection,
  ContainersDetail,
  DependenciesSection,
  ImagesSection,
  NetworksSection,
  ServicesSection,
  TmpfsSection,
  VolumesSection,
} from "@/components/applications/analysis-sections";
import {
  displayName,
  KindBadge,
  MissingBadge,
  SourceBadge,
  UnprotectedBadge,
} from "@/components/applications/app-badges";
import { ComposeView } from "@/components/applications/compose-view";
import { ApplicationBackups } from "@/components/backups/application-backups";
import { BackUpNowButton } from "@/components/backups/back-up-now";
import { BackupSettingsCard } from "@/components/backups/backup-settings-card";
import { DeleteApplicationButton } from "@/components/applications/delete-application-dialog";
import { EditMetadataDialog } from "@/components/applications/edit-metadata-dialog";
import { UnprotectedData } from "@/components/applications/unprotected-data";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isApiError } from "@/lib/api/client";
import {
  PERMISSION_APPLICATION_MANAGE,
  PERMISSION_APPLICATION_READ,
  PERMISSION_HOST_READ,
  type ApplicationDetail,
} from "@/lib/api/fleet-schemas";
import { useApplication } from "@/lib/api/hooks";
import {
  PERMISSION_BACKUP_EXECUTE,
  PERMISSION_BACKUP_READ,
  PERMISSION_POLICY_READ,
} from "@/lib/api/protection-schemas";
import { formatDateTime, formatRelative } from "@/lib/format";

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-0.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

function Header({ app }: { app: ApplicationDetail }) {
  const me = useCurrentUser();
  const router = useRouter();
  const canManage = hasPermission(me, PERMISSION_APPLICATION_MANAGE);
  const canHosts = hasPermission(me, PERMISSION_HOST_READ);
  const canBackup = hasPermission(me, PERMISSION_BACKUP_EXECUTE);
  const shown = displayName(app);
  const a = app.analysis;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1.5">
          <h1 className="text-2xl font-semibold tracking-tight">{shown}</h1>
          {shown !== app.name && <p className="font-mono text-sm text-muted-foreground">{app.name}</p>}
          <div className="flex flex-wrap items-center gap-2">
            <KindBadge kind={app.kind} />
            <SourceBadge source={app.source} />
            <UnprotectedBadge count={app.unprotected_high} />
            {app.missing_since && <MissingBadge since={formatDateTime(app.missing_since)} />}
          </div>
        </div>
        {(canManage || canBackup) && (
          <div className="flex flex-wrap items-center gap-2">
            {canBackup && <BackUpNowButton applicationId={app.id} name={shown} />}
            {canManage && <EditMetadataDialog app={app} />}
            {canManage && app.kind === "manual" && (
              <DeleteApplicationButton
                id={app.id}
                name={shown}
                size="sm"
                onDeleted={() => router.push("/applications")}
              />
            )}
          </div>
        )}
      </div>
      <dl className="grid grid-cols-2 gap-4 rounded-lg border p-4 sm:grid-cols-3 lg:grid-cols-6">
        <Meta label="Host">
          {canHosts ? (
            <Link href={`/hosts/${app.host_id}`} className="underline-offset-4 hover:underline">
              {app.hostname}
            </Link>
          ) : (
            app.hostname
          )}
        </Meta>
        <Meta label="Owner">{app.owner ?? <span className="text-muted-foreground">Not set</span>}</Meta>
        <Meta label="Environment">
          {app.environment ?? <span className="text-muted-foreground">Not set</span>}
        </Meta>
        <Meta label="Criticality">
          {app.criticality ?? <span className="text-muted-foreground">Not set</span>}
        </Meta>
        <Meta label="Last seen">
          <time dateTime={app.last_seen_at} title={formatDateTime(app.last_seen_at)}>
            {formatRelative(app.last_seen_at)}
          </time>
        </Meta>
        <Meta label="Secrets detected">{app.secrets_count}</Meta>
        {a?.working_dir && (
          <div className="col-span-2 space-y-0.5 sm:col-span-3 lg:col-span-6">
            <dt className="text-xs text-muted-foreground">Project directory</dt>
            <dd className="font-mono text-xs break-all">{a.working_dir}</dd>
          </div>
        )}
        {app.kind === "manual" && app.manual_containers.length > 0 && (
          <div className="col-span-2 space-y-0.5 sm:col-span-3 lg:col-span-6">
            <dt className="text-xs text-muted-foreground">Grouped containers</dt>
            <dd className="flex flex-wrap gap-1">
              {app.manual_containers.map((c) => (
                <Badge key={c} variant="outline" className="font-mono">
                  {c}
                </Badge>
              ))}
            </dd>
          </div>
        )}
      </dl>
    </div>
  );
}

function DetailBody({ id }: { id: string }) {
  const app = useApplication(id);

  if (app.isPending) return <RowsSkeleton label="Loading application…" />;
  if (app.isError && !app.data) {
    if (isApiError(app.error) && app.error.status === 404) {
      return (
        <Alert>
          <AlertTitle>Application not found</AlertTitle>
          <AlertDescription>It may have been deleted or regrouped.</AlertDescription>
        </Alert>
      );
    }
    return <QueryError title="Could not load the application" error={app.error} onRetry={() => void app.refetch()} />;
  }

  const d = app.data;
  const a = d.analysis;

  return (
    <div className="space-y-6">
      <Header app={d} />
      {a ? (
        <UnprotectedData items={a.unprotected} />
      ) : (
        <Alert>
          <AlertTitle>Not in the latest inventory</AlertTitle>
          <AlertDescription>
            This application was not found in the host&apos;s latest discovery
            {d.missing_since ? ` (missing since ${formatDateTime(d.missing_since)})` : ""}, so no
            analysis is available.
          </AlertDescription>
        </Alert>
      )}
      <DetailTabs app={d} />
    </div>
  );
}

function DetailTabs({ app: d }: { app: ApplicationDetail }) {
  const me = useCurrentUser();
  const a = d.analysis;
  const canBackups = hasPermission(me, PERMISSION_BACKUP_READ);
  const canSettings = hasPermission(me, PERMISSION_POLICY_READ);
  const first = a ? "resources" : canBackups ? "backups" : canSettings ? "backup-settings" : null;
  if (!first) return null;

  return (
    <Tabs defaultValue={first}>
      <TabsList>
        {a && <TabsTrigger value="resources">Resources</TabsTrigger>}
        {a && <TabsTrigger value="containers">Containers ({d.containers_detail.length})</TabsTrigger>}
        {a && <TabsTrigger value="compose">Compose</TabsTrigger>}
        {canBackups && <TabsTrigger value="backups">Backups</TabsTrigger>}
        {canSettings && <TabsTrigger value="backup-settings">Backup settings</TabsTrigger>}
      </TabsList>
      {a && (
        <TabsContent value="resources" className="mt-4 space-y-6">
          {a.unprotected.length === 0 && (
            <Alert role="status">
              <AlertTitle>No unprotected data detected</AlertTitle>
              <AlertDescription>
                Every writable path found in these containers is backed by a volume or bind mount.
              </AlertDescription>
            </Alert>
          )}
          <ServicesSection analysis={a} />
          <VolumesSection analysis={a} />
          <BindMountsSection analysis={a} />
          <TmpfsSection analysis={a} />
          <NetworksSection analysis={a} />
          <ImagesSection analysis={a} />
          <DependenciesSection analysis={a} />
        </TabsContent>
      )}
      {a && (
        <TabsContent value="containers" className="mt-4">
          <p className="mb-4 text-sm text-muted-foreground">
            Collected {formatDateTime(d.collected_at)}. Sensitive environment values are masked by
            the server.
          </p>
          <ContainersDetail containers={d.containers_detail} />
        </TabsContent>
      )}
      {a && (
        <TabsContent value="compose" className="mt-4">
          <ComposeView applicationId={d.id} />
        </TabsContent>
      )}
      {canBackups && (
        <TabsContent value="backups" className="mt-4">
          <ApplicationBackups applicationId={d.id} />
        </TabsContent>
      )}
      {canSettings && (
        <TabsContent value="backup-settings" className="mt-4">
          <BackupSettingsCard app={d} />
        </TabsContent>
      )}
    </Tabs>
  );
}

export function ApplicationDetailView({ id }: { id: string }) {
  const me = useCurrentUser();
  return (
    <div className="space-y-4">
      <Link
        href="/applications"
        className={buttonVariants({ variant: "ghost", size: "sm", className: "-ml-2.5" })}
      >
        <ArrowLeftIcon aria-hidden="true" /> All applications
      </Link>
      {hasPermission(me, PERMISSION_APPLICATION_READ) ? (
        <DetailBody id={id} />
      ) : (
        <AccessDenied what="Viewing applications" permission={PERMISSION_APPLICATION_READ} />
      )}
    </div>
  );
}
