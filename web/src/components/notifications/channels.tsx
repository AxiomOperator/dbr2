// SPDX-License-Identifier: Apache-2.0
"use client";

// Notification channels (Phase 7): email (via the SMTP settings) and
// webhooks (optional HMAC signing secret, write-only: keep / replace /
// remove), each with an event filter and minimum severity; "Send test" and
// the delivery history.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  CircleCheckIcon,
  CircleDashedIcon,
  CircleXIcon,
  HistoryIcon,
  MailIcon,
  PencilIcon,
  PlusIcon,
  SendIcon,
  Trash2Icon,
  WebhookIcon,
} from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { SeverityBadge } from "@/components/backups/backup-badges";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { EventPicker } from "@/components/notifications/event-picker";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  CreateChannelRequestSchema,
  UpdateChannelRequestSchema,
  type ChannelKind,
  type CreateChannelRequest,
  type Delivery,
  type NotificationChannel,
  type UpdateChannelRequest,
} from "@/lib/api/policy-schemas";
import { PERMISSION_POLICY_MANAGE, PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";
import { useNotificationChannels, useNotificationDeliveries } from "@/lib/api/hooks";
import { formatDateTime, formatRelative } from "@/lib/format";
import {
  eventsSummary,
  looksLikeEmail,
  MIN_SEVERITY_HELP,
  MIN_WEBHOOK_SECRET,
  normalizeEvents,
  secretField,
  SEVERITIES,
  splitRecipients,
  webhookUrlError,
  type SecretAction,
  type Severity,
} from "@/lib/notifications";

// ---------------------------------------------------------------------------
// Draft <-> request (pure, tested)
// ---------------------------------------------------------------------------

export interface ChannelDraft {
  kind: ChannelKind;
  name: string;
  enabled: boolean;
  recipients: string;
  url: string;
  /** New channels: "replace" sets a secret, "keep" means none. */
  secretAction: SecretAction;
  secret: string;
  events: string[];
  minSeverity: Severity;
}

export function newChannelDraft(kind: ChannelKind = "email"): ChannelDraft {
  return {
    kind,
    name: "",
    enabled: true,
    recipients: "",
    url: "",
    secretAction: "keep",
    secret: "",
    events: ["backup.failed", "restore.failed", "agent.offline", "contract.violated"],
    minSeverity: "warning",
  };
}

export function channelToDraft(c: NotificationChannel): ChannelDraft {
  return {
    kind: c.kind,
    name: c.name,
    enabled: c.enabled,
    recipients: c.config.to.join("\n"),
    url: c.config.url ?? "",
    secretAction: "keep",
    secret: "",
    events: [...c.events],
    minSeverity: c.min_severity,
  };
}

type Built<T> = { ok: true; value: T } | { ok: false; error: string };

function common(d: ChannelDraft): Built<Omit<UpdateChannelRequest, "secret">> {
  const events = normalizeEvents(d.events.filter((e) => e !== "*"));
  if (!events.ok) return events;
  let config: UpdateChannelRequest["config"];
  if (d.kind === "email") {
    const to = splitRecipients(d.recipients);
    if (to.length === 0) return { ok: false, error: "Enter at least one recipient address." };
    const bad = to.find((t) => !looksLikeEmail(t));
    if (bad) return { ok: false, error: `“${bad}” is not an email address.` };
    config = { to };
  } else {
    const err = webhookUrlError(d.url);
    if (err) return { ok: false, error: err };
    config = { url: d.url.trim() };
  }
  return { ok: true, value: { name: d.name, enabled: d.enabled, config, events: events.events, min_severity: d.minSeverity } };
}

function secretOf(d: ChannelDraft): Built<string | undefined> {
  if (d.kind !== "webhook") return { ok: true, value: undefined };
  const s = secretField(d.secretAction, d.secret);
  if (s !== undefined && s !== "" && s.length < MIN_WEBHOOK_SECRET) {
    return { ok: false, error: `The signing secret must be at least ${MIN_WEBHOOK_SECRET} characters.` };
  }
  return { ok: true, value: s };
}

/** POST body: a secret only when one is set ("keep" = none). */
export function draftToCreateRequest(d: ChannelDraft): Built<CreateChannelRequest> {
  const base = common(d);
  if (!base.ok) return base;
  const secret = secretOf({ ...d, secretAction: d.secretAction === "remove" ? "keep" : d.secretAction });
  if (!secret.ok) return secret;
  const parsed = CreateChannelRequestSchema.safeParse({
    kind: d.kind,
    ...base.value,
    ...(secret.value ? { secret: secret.value } : {}),
  });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

/** PUT body: `secret` omitted = keep, "" = remove, value = replace. */
export function draftToUpdateRequest(d: ChannelDraft): Built<UpdateChannelRequest> {
  const base = common(d);
  if (!base.ok) return base;
  const secret = secretOf(d);
  if (!secret.ok) return secret;
  const parsed = UpdateChannelRequestSchema.safeParse({
    ...base.value,
    ...(secret.value !== undefined ? { secret: secret.value } : {}),
  });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

// ---------------------------------------------------------------------------
// Form dialog
// ---------------------------------------------------------------------------

export const KIND_LABEL: Record<ChannelKind, string> = { email: "Email", webhook: "Webhook" };

export function KindBadge({ kind }: { kind: ChannelKind }) {
  return (
    <Badge variant="outline">
      {kind === "email" ? <MailIcon aria-hidden="true" /> : <WebhookIcon aria-hidden="true" />} {KIND_LABEL[kind]}
    </Badge>
  );
}

function SecretFields({
  draft,
  set,
  secretSet,
  disabled,
}: {
  draft: ChannelDraft;
  set: (p: Partial<ChannelDraft>) => void;
  /** Editing a channel that has a stored secret (null when creating). */
  secretSet: boolean | null;
  disabled: boolean;
}) {
  const id = useId();
  const creating = secretSet === null;
  const options: { value: SecretAction; label: string }[] = creating
    ? [
        { value: "keep", label: "No signing secret" },
        { value: "replace", label: "Sign requests with a secret" },
      ]
    : secretSet
      ? [
          { value: "keep", label: "Keep the stored secret" },
          { value: "replace", label: "Replace it" },
          { value: "remove", label: "Remove it (unsigned requests)" },
        ]
      : [
          { value: "keep", label: "No signing secret" },
          { value: "replace", label: "Set a secret" },
        ];
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium">
        Signing secret{" "}
        {!creating && (
          <span className="font-normal text-muted-foreground">({secretSet ? "a secret is stored" : "none stored"})</span>
        )}
      </legend>
      <div className="flex flex-col gap-1.5" role="radiogroup" aria-label="Signing secret">
        {options.map((o) => (
          <label key={o.value} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name={`${id}-secret-action`}
              value={o.value}
              checked={draft.secretAction === o.value}
              onChange={() => set({ secretAction: o.value, secret: o.value === "replace" ? draft.secret : "" })}
              disabled={disabled}
              className="size-4 accent-primary"
            />
            {o.label}
          </label>
        ))}
      </div>
      {draft.secretAction === "replace" && (
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-secret`}>{creating || !secretSet ? "Secret" : "New secret"}</Label>
          <Input
            id={`${id}-secret`}
            type="password"
            autoComplete="new-password"
            value={draft.secret}
            onChange={(e) => set({ secret: e.target.value })}
            minLength={MIN_WEBHOOK_SECRET}
            maxLength={500}
            disabled={disabled}
            aria-describedby={`${id}-secret-help`}
          />
        </div>
      )}
      <p id={`${id}-secret-help`} className="text-xs text-muted-foreground">
        At least {MIN_WEBHOOK_SECRET} characters. Requests then carry{" "}
        <code className="font-mono">X-DBR2-Signature: sha256=HMAC(secret, timestamp + &quot;.&quot; + body)</code>. The
        secret is write-only: DBR² never shows it again.
      </p>
    </fieldset>
  );
}

/** Create (no `channel`) or edit a notification channel (requires `policy.manage`). */
export function ChannelFormDialog({ channel }: { channel?: NotificationChannel }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<ChannelDraft>(() => (channel ? channelToDraft(channel) : newChannelDraft()));
  const [error, setError] = useState<string | null>(null);
  const set = (p: Partial<ChannelDraft>) => setDraft((d) => ({ ...d, ...p }));
  const save = useMutation({
    mutationFn: async (d: ChannelDraft) => {
      if (channel) {
        const req = draftToUpdateRequest(d);
        if (!req.ok) throw new Error(req.error);
        return api.updateNotificationChannel(channel.id, req.value);
      }
      const req = draftToCreateRequest(d);
      if (!req.ok) throw new Error(req.error);
      return api.createNotificationChannel(req.value);
    },
    onSuccess: async (saved) => {
      setOpen(false);
      toast({ title: channel ? `Channel ${saved.name} saved` : `Channel ${saved.name} created`, description: "Use “Send test” to check delivery." });
      await queryClient.invalidateQueries({ queryKey: queryKeys.notificationChannels });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const pending = save.isPending;

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const check = channel ? draftToUpdateRequest(draft) : draftToCreateRequest(draft);
    if (!check.ok) {
      setError(check.error);
      return;
    }
    save.mutate(draft);
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setDraft(channel ? channelToDraft(channel) : newChannelDraft());
          setError(null);
          save.reset();
        }
      }}
    >
      <DialogTrigger asChild>
        {channel ? (
          <Button size="xs" variant="outline" aria-label={`Edit channel ${channel.name}`}>
            <PencilIcon aria-hidden="true" /> Edit
          </Button>
        ) : (
          <Button>
            <PlusIcon aria-hidden="true" /> Add channel
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{channel ? `Edit ${channel.name}` : "Add notification channel"}</DialogTitle>
          <DialogDescription>
            Matching notifications are delivered with retries (1 min, 5 min, 30 min, 2 h, 6 h; failed after 6 attempts).
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate className="space-y-5">
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor={`${id}-kind`}>Type</Label>
              <Select
                value={draft.kind}
                onValueChange={(v) => set({ kind: v as ChannelKind, secretAction: "keep", secret: "" })}
                disabled={pending || Boolean(channel)}
              >
                <SelectTrigger id={`${id}-kind`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="email">Email (uses the SMTP settings)</SelectItem>
                  <SelectItem value="webhook">Webhook (HTTPS POST, JSON)</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-name`}>Name</Label>
              <Input
                id={`${id}-name`}
                value={draft.name}
                onChange={(e) => set({ name: e.target.value })}
                placeholder={draft.kind === "email" ? "e.g. Ops email" : "e.g. Teams webhook"}
                maxLength={100}
                aria-required="true"
                disabled={pending}
              />
            </div>
          </div>
          {draft.kind === "email" ? (
            <div className="space-y-2">
              <Label htmlFor={`${id}-to`}>Recipients</Label>
              <Textarea
                id={`${id}-to`}
                value={draft.recipients}
                onChange={(e) => set({ recipients: e.target.value })}
                placeholder={"ops@example.com\noncall@example.com"}
                rows={3}
                aria-describedby={`${id}-to-help`}
                disabled={pending}
              />
              <p id={`${id}-to-help`} className="text-xs text-muted-foreground">
                One address per line (or separated by commas); at most 50.
              </p>
            </div>
          ) : (
            <>
              <div className="space-y-2">
                <Label htmlFor={`${id}-url`}>Webhook URL</Label>
                <Input
                  id={`${id}-url`}
                  value={draft.url}
                  onChange={(e) => set({ url: e.target.value })}
                  placeholder="https://hooks.example.com/dbr2"
                  className="font-mono text-xs"
                  spellCheck={false}
                  maxLength={2000}
                  aria-describedby={`${id}-url-help`}
                  disabled={pending}
                />
                <p id={`${id}-url-help`} className="text-xs text-muted-foreground">
                  https is required (plain http only for localhost). Redirects are not followed.
                </p>
              </div>
              <SecretFields draft={draft} set={set} secretSet={channel ? channel.secret_set : null} disabled={pending} />
            </>
          )}
          <EventPicker value={draft.events} onChange={(events) => set({ events })} disabled={pending} />
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor={`${id}-sev`}>Minimum severity</Label>
              <Select value={draft.minSeverity} onValueChange={(v) => set({ minSeverity: v as Severity })} disabled={pending}>
                <SelectTrigger id={`${id}-sev`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SEVERITIES.map((s) => (
                    <SelectItem key={s} value={s}>
                      {s.charAt(0).toUpperCase() + s.slice(1)}: {MIN_SEVERITY_HELP[s].toLowerCase()}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-end gap-2 pb-1">
              <Checkbox
                id={`${id}-enabled`}
                checked={draft.enabled}
                onCheckedChange={(v) => set({ enabled: v === true })}
                disabled={pending}
              />
              <Label htmlFor={`${id}-enabled`} className="font-normal">
                Enabled
              </Label>
            </div>
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={pending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={pending}>
              {pending ? "Saving…" : channel ? "Save channel" : "Add channel"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Row actions
// ---------------------------------------------------------------------------

export function SendTestButton({ channel }: { channel: NotificationChannel }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<string | null>(null);
  const test = useMutation({
    mutationFn: () => api.testNotificationChannel(channel.id),
    onSuccess: async (r) => {
      const text = r.delivered
        ? `Delivered in ${r.duration_ms} ms`
        : `Not delivered${r.error ? `: ${r.error}` : ""}`;
      setResult(text);
      toast({
        title: r.delivered ? `Test message delivered to ${channel.name}` : `Test message to ${channel.name} failed`,
        description: r.delivered ? `The channel answered in ${r.duration_ms} ms.` : (r.error ?? "No error was reported."),
        variant: r.delivered ? "default" : "destructive",
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.notificationChannels });
    },
    onError: (err) => {
      setResult(null);
      toast({ title: `Could not test ${channel.name}`, description: actionErrorMessage(err), variant: "destructive" });
    },
  });
  return (
    <div className="space-y-1">
      <Button
        size="xs"
        variant="outline"
        onClick={() => test.mutate()}
        disabled={test.isPending}
        aria-label={`Send a test message to ${channel.name}`}
      >
        <SendIcon aria-hidden="true" /> {test.isPending ? "Sending…" : "Send test"}
      </Button>
      {result && (
        <p className="max-w-48 text-xs break-words text-muted-foreground" role="status" data-testid="test-result">
          {result}
        </p>
      )}
    </div>
  );
}

function DeleteChannelButton({ channel }: { channel: NotificationChannel }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const queryClient = useQueryClient();
  const del = useMutation({
    mutationFn: () => api.deleteNotificationChannel(channel.id),
    onSuccess: async () => {
      setOpen(false);
      toast({ title: `Channel ${channel.name} deleted` });
      await queryClient.invalidateQueries({ queryKey: queryKeys.notificationChannels });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setError(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size="xs" variant="destructive" aria-label={`Delete channel ${channel.name}`}>
          <Trash2Icon aria-hidden="true" /> Delete
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete channel {channel.name}?</DialogTitle>
          <DialogDescription>
            Its delivery history is deleted too. Alerts themselves are kept and still shown in the console.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={del.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button variant="destructive" onClick={() => del.mutate()} disabled={del.isPending}>
            {del.isPending ? "Deleting…" : "Delete channel"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function DeliveryStateBadge({ state }: { state: Delivery["state"] }) {
  switch (state) {
    case "sent":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" /> Sent
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive">
          <CircleXIcon aria-hidden="true" /> Failed
        </Badge>
      );
    case "pending":
      return (
        <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
          <CircleDashedIcon aria-hidden="true" /> Retrying
        </Badge>
      );
  }
}

function DeliveriesTable({ channelId }: { channelId: string }) {
  const deliveries = useNotificationDeliveries(channelId);
  if (deliveries.isPending) return <RowsSkeleton label="Loading deliveries…" rows={4} />;
  if (deliveries.isError && !deliveries.data) {
    return <QueryError title="Could not load the deliveries" error={deliveries.error} onRetry={() => void deliveries.refetch()} />;
  }
  if (deliveries.data.length === 0) return <p className="text-sm text-muted-foreground">Nothing was delivered through this channel yet.</p>;
  return (
    <div className="rounded-lg border">
      <Table aria-label="Deliveries">
        <TableHeader>
          <TableRow>
            {["When", "Event", "State", "Attempts", "Details"].map((h) => (
              <TableHead key={h} scope="col">
                {h}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {deliveries.data.map((d) => (
            <TableRow key={d.id} data-state={d.state}>
              <TableCell className="align-top whitespace-nowrap">
                <time dateTime={d.created_at} title={formatDateTime(d.created_at)}>
                  {formatRelative(d.created_at)}
                </time>
              </TableCell>
              <TableCell className="align-top">
                <div className="font-mono text-xs">{d.event_type}</div>
                <SeverityBadge severity={d.severity} />
              </TableCell>
              <TableCell className="align-top">
                <DeliveryStateBadge state={d.state} />
              </TableCell>
              <TableCell className="align-top tabular-nums">{d.attempts}</TableCell>
              <TableCell className="max-w-80 align-top text-xs break-words whitespace-normal">
                <p>{d.message}</p>
                {d.sent_at && <p className="text-muted-foreground">Sent {formatDateTime(d.sent_at)}</p>}
                {d.next_attempt_at && <p className="text-muted-foreground">Next attempt {formatRelative(d.next_attempt_at)}</p>}
                {d.last_error && <p className="text-destructive">{d.last_error}</p>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

/** Delivery history in a side drawer. */
export function DeliveriesDrawer({ channel }: { channel: NotificationChannel }) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="xs" variant="outline" aria-label={`Deliveries of ${channel.name}`}>
          <HistoryIcon aria-hidden="true" /> Deliveries
        </Button>
      </DialogTrigger>
      <DialogContent className="top-0 right-0 left-auto h-full max-h-screen w-full translate-x-0 translate-y-0 content-start overflow-y-auto rounded-none sm:max-w-2xl data-open:zoom-in-100 data-closed:zoom-out-100">
        <DialogHeader>
          <DialogTitle>Deliveries: {channel.name}</DialogTitle>
          <DialogDescription>Most recent first, with attempts and the last error. Refreshes every 15 s.</DialogDescription>
        </DialogHeader>
        {open && <DeliveriesTable channelId={channel.id} />}
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Panel
// ---------------------------------------------------------------------------

export function ChannelsPanel() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_POLICY_READ);
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  const channels = useNotificationChannels({ enabled: canRead });
  if (!canRead) return <AccessDenied what="Viewing notification channels" permission={PERMISSION_POLICY_READ} />;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Where alerts are sent: email (through the SMTP relay) or webhooks. Each channel receives the events it
          subscribes to at or above its minimum severity. Secrets are never shown.
        </p>
        {canManage && <ChannelFormDialog />}
      </div>
      {channels.isPending ? (
        <RowsSkeleton label="Loading channels…" rows={3} />
      ) : channels.isError && !channels.data ? (
        <QueryError title="Could not load the channels" error={channels.error} onRetry={() => void channels.refetch()} />
      ) : (
        <div className="rounded-lg border">
          <Table aria-label="Notification channels">
            <TableHeader>
              <TableRow>
                {["Channel", "Target", "Events", "Minimum severity", "Last delivery", ""].map((h, i) => (
                  <TableHead key={i} scope="col">
                    {h || <span className="sr-only">Actions</span>}
                  </TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {channels.data.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={6} className="h-24 text-center whitespace-normal text-muted-foreground">
                    No channels yet: alerts are only shown in the console.{canManage ? " Use “Add channel”." : ""}
                  </TableCell>
                </TableRow>
              ) : (
                channels.data.map((c) => (
                  <TableRow key={c.id} data-kind={c.kind}>
                    <TableCell className="align-top">
                      <div className="font-medium">{c.name}</div>
                      <div className="mt-1 flex flex-wrap gap-1">
                        <KindBadge kind={c.kind} />
                        {!c.enabled && <Badge variant="outline">Disabled</Badge>}
                        {c.kind === "webhook" && c.secret_set && <Badge variant="outline">Signed</Badge>}
                      </div>
                    </TableCell>
                    <TableCell className="max-w-64 align-top font-mono text-xs break-all whitespace-normal">
                      {c.kind === "email" ? c.config.to.join(", ") : c.config.url}
                    </TableCell>
                    <TableCell className="max-w-48 align-top text-xs break-words whitespace-normal">
                      {eventsSummary(c.events)}
                    </TableCell>
                    <TableCell className="align-top">
                      <SeverityBadge severity={c.min_severity} />
                    </TableCell>
                    <TableCell className="max-w-64 align-top text-xs whitespace-normal">
                      {c.last_delivery_at ? (
                        <time dateTime={c.last_delivery_at} title={formatDateTime(c.last_delivery_at)}>
                          {formatRelative(c.last_delivery_at)}
                        </time>
                      ) : (
                        <span className="text-muted-foreground">Never</span>
                      )}
                      {c.last_error && (
                        <p className="mt-1 break-words text-destructive" data-testid="last-error">
                          <span className="sr-only">Last error: </span>
                          {c.last_error}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="flex flex-col items-stretch gap-1">
                        {canManage && <SendTestButton channel={c} />}
                        <DeliveriesDrawer channel={c} />
                        {canManage && <ChannelFormDialog channel={c} />}
                        {canManage && <DeleteChannelButton channel={c} />}
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
