// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { BanIcon, CircleCheckIcon, KeyRoundIcon, PlusIcon, Trash2Icon, UserCogIcon } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
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
import { useGroupMappings, useRoles, useUsers } from "@/lib/api/hooks";
import {
  ENTRA_PROVIDER,
  GroupMappingRequestSchema,
  PERMISSION_USER_MANAGE,
  PERMISSION_USER_READ,
  ROLE_SOURCE_LABEL,
  SetRolesRequestSchema,
  SetStatusRequestSchema,
  type GroupMapping,
  type Role,
  type User,
} from "@/lib/api/users-schemas";
import { formatDateTime, formatRelative } from "@/lib/format";

/** Roles granted manually (the only ones the console can change). */
export const manualRoles = (u: User): string[] => u.roles.filter((r) => r.source === "manual").map((r) => r.role);

const roleName = (roles: Role[] | undefined, role: string) => roles?.find((r) => r.role === role)?.display_name ?? role;

function RoleBadges({ user, roles }: { user: User; roles: Role[] | undefined }) {
  if (user.roles.length === 0) return <span className="text-muted-foreground">No roles</span>;
  return (
    <ul className="flex flex-wrap gap-1" aria-label={`Roles of ${user.display_name}`}>
      {user.roles.map((r) => (
        <li key={`${r.role}-${r.source}`}>
          <Badge variant={r.source === "manual" ? "secondary" : "outline"} title={ROLE_SOURCE_LABEL[r.source]}>
            {roleName(roles, r.role)}
            <span className="text-muted-foreground">
              {r.source === "oidc_group" ? " · group" : r.source === "master_admin" ? " · built-in" : ""}
            </span>
          </Badge>
        </li>
      ))}
    </ul>
  );
}

function RolesDialog({ user, roles }: { user: User; roles: Role[] }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const fromGroups = new Set(user.roles.filter((r) => r.source === "oidc_group").map((r) => r.role));

  const save = useMutation({
    mutationFn: () => api.setUserRoles(user.id, { roles: selected, reason }),
    onSuccess: async () => {
      toast({ title: `Roles of ${user.display_name} updated` });
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.users });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const parsed = SetRolesRequestSchema.safeParse({ roles: selected, reason });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    setError(null);
    save.mutate();
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (o) {
          setSelected(manualRoles(user));
          setReason("");
          setError(null);
          save.reset();
        }
        setOpen(o);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant="outline" aria-label={`Edit roles of ${user.display_name}`}>
          <UserCogIcon aria-hidden="true" /> Roles
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>Roles of {user.display_name}</DialogTitle>
            <DialogDescription>
              Choose the roles granted manually. Roles from Entra ID group mappings are managed by the group and
              are synced at each sign-in.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <fieldset className="space-y-2">
            <legend className="sr-only">Roles</legend>
            {roles.map((r) => {
              const cid = `${id}-${r.role}`;
              const group = fromGroups.has(r.role);
              return (
                <div key={r.role} className="flex items-start gap-2">
                  <Checkbox
                    id={cid}
                    checked={selected.includes(r.role)}
                    onCheckedChange={(v) =>
                      setSelected((cur) => (v === true ? [...new Set([...cur, r.role])] : cur.filter((x) => x !== r.role)))
                    }
                    disabled={save.isPending}
                  />
                  <div className="space-y-0.5">
                    <Label htmlFor={cid} className="font-medium">
                      {r.display_name}
                      {group && <span className="font-normal text-muted-foreground"> (also from a group)</span>}
                    </Label>
                    <p className="text-xs text-muted-foreground">{r.description}</p>
                  </div>
                </div>
              );
            })}
          </fieldset>
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-reason`}>Reason (audit log)</Label>
            <Textarea id={`${id}-reason`} value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} rows={2} />
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={save.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={save.isPending || reason.trim() === ""}>
              {save.isPending ? "Saving…" : "Save roles"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function StatusDialog({ user }: { user: User }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const disable = !user.disabled;

  const save = useMutation({
    mutationFn: () => api.setUserStatus(user.id, { disabled: disable, reason }),
    onSuccess: async () => {
      toast({ title: `${user.display_name} ${disable ? "disabled" : "enabled"}` });
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.users });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const parsed = SetStatusRequestSchema.safeParse({ disabled: disable, reason });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    save.mutate();
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (o) {
          setReason("");
          setError(null);
          save.reset();
        }
        setOpen(o);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant={disable ? "outline" : "secondary"} aria-label={`${disable ? "Disable" : "Enable"} ${user.display_name}`}>
          {disable ? <BanIcon aria-hidden="true" /> : <CircleCheckIcon aria-hidden="true" />} {disable ? "Disable" : "Enable"}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>
              {disable ? "Disable" : "Enable"} {user.display_name}
            </DialogTitle>
            <DialogDescription>
              {disable
                ? "Disabling signs the user out everywhere (all sessions are revoked) and blocks new sign-ins."
                : "The user can sign in again; roles apply as before."}
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-reason`}>Reason (audit log)</Label>
            <Textarea id={`${id}-reason`} value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} rows={2} />
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={save.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" variant={disable ? "destructive" : "default"} disabled={save.isPending || reason.trim() === ""}>
              {save.isPending ? "Saving…" : disable ? "Disable user" : "Enable user"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function UsersTable({ canManage }: { canManage: boolean }) {
  const users = useUsers();
  const roles = useRoles();
  if (users.isPending) return <RowsSkeleton label="Loading users…" />;
  if (users.isError && !users.data) {
    return <QueryError title="Could not load users" error={users.error} onRetry={() => void users.refetch()} />;
  }
  return (
    <div className="rounded-lg border">
      <Table aria-label="Users">
        <TableHeader>
          <TableRow>
            <TableHead scope="col">User</TableHead>
            <TableHead scope="col">Sign-in</TableHead>
            <TableHead scope="col">Status</TableHead>
            <TableHead scope="col">Roles</TableHead>
            <TableHead scope="col">Last sign-in</TableHead>
            {canManage && (
              <TableHead scope="col">
                <span className="sr-only">Actions</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {users.data.map((u) => (
            <TableRow key={u.id} data-disabled={u.disabled}>
              <TableCell className="align-top">
                <div className="font-medium">{u.display_name}</div>
                <div className="text-xs text-muted-foreground">{u.email ?? u.username}</div>
              </TableCell>
              <TableCell className="align-top">
                {u.kind === "master_admin" ? (
                  <Badge variant="outline">
                    <KeyRoundIcon aria-hidden="true" /> Master admin
                  </Badge>
                ) : (
                  <Badge variant="outline">Entra ID</Badge>
                )}
              </TableCell>
              <TableCell className="align-top">
                {u.disabled ? (
                  <Badge variant="destructive">
                    <BanIcon aria-hidden="true" /> Disabled
                  </Badge>
                ) : (
                  <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
                    <CircleCheckIcon aria-hidden="true" /> Active
                  </Badge>
                )}
              </TableCell>
              <TableCell className="align-top whitespace-normal">
                <RoleBadges user={u} roles={roles.data} />
              </TableCell>
              <TableCell className="align-top">
                {u.last_login_at ? (
                  <time dateTime={u.last_login_at} title={formatDateTime(u.last_login_at)}>
                    {formatRelative(u.last_login_at)}
                  </time>
                ) : (
                  <span className="text-muted-foreground">Never</span>
                )}
              </TableCell>
              {canManage && (
                <TableCell className="align-top">
                  {u.kind === "master_admin" ? (
                    <span className="text-xs text-muted-foreground">Built-in; cannot be changed</span>
                  ) : (
                    <div className="flex flex-wrap gap-2">
                      {roles.data && <RolesDialog user={u} roles={roles.data} />}
                      <StatusDialog user={u} />
                    </div>
                  )}
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function RolesReference() {
  const roles = useRoles();
  const [open, setOpen] = useState(false);
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <Button variant="ghost" size="sm" className="-ml-2.5">
          {open ? "Hide" : "Show"} what each role may do
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        {roles.data && (
          <dl className="mt-2 grid gap-3 sm:grid-cols-2">
            {roles.data.map((r) => (
              <div key={r.role} className="rounded-lg border p-3 text-sm">
                <dt className="font-medium">
                  {r.display_name} <span className="font-mono text-xs text-muted-foreground">{r.role}</span>
                </dt>
                <dd className="mt-1 space-y-1.5">
                  <p className="text-muted-foreground">{r.description}</p>
                  <p className="font-mono text-xs break-words">{r.permissions.join(", ")}</p>
                </dd>
              </div>
            ))}
          </dl>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}

function AddMappingDialog({ roles }: { roles: Role[] }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [groupId, setGroupId] = useState("");
  const [role, setRole] = useState("");
  const [error, setError] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: api.addGroupMapping,
    onSuccess: async () => {
      toast({ title: "Group mapping added", description: "Members get the role at their next sign-in." });
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.groupMappings });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const parsed = GroupMappingRequestSchema.safeParse({ provider: ENTRA_PROVIDER, group_id: groupId, role });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form.");
      return;
    }
    setError(null);
    add.mutate(parsed.data);
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (o) {
          setGroupId("");
          setRole("");
          setError(null);
          add.reset();
        }
        setOpen(o);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <PlusIcon aria-hidden="true" /> Add mapping
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="space-y-4" noValidate>
          <DialogHeader>
            <DialogTitle>Map an Entra ID group to a role</DialogTitle>
            <DialogDescription>
              Members of the group get the role when they sign in with Microsoft. Use the group&apos;s object ID
              from the Entra admin center.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-group`}>Group object ID</Label>
            <Input
              id={`${id}-group`}
              value={groupId}
              onChange={(e) => setGroupId(e.target.value)}
              placeholder="00000000-0000-0000-0000-000000000000"
              className="font-mono"
              maxLength={256}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${id}-role`}>Role</Label>
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger id={`${id}-role`} className="w-full">
                <SelectValue placeholder="Choose a role" />
              </SelectTrigger>
              <SelectContent>
                {roles.map((r) => (
                  <SelectItem key={r.role} value={r.role}>
                    {r.display_name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={add.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={add.isPending}>
              {add.isPending ? "Adding…" : "Add mapping"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RemoveMappingButton({ mapping, roleLabel }: { mapping: GroupMapping; roleLabel: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const remove = useMutation({
    mutationFn: () => api.removeGroupMapping(mapping),
    onSuccess: async () => {
      toast({ title: "Group mapping removed", description: "Members lose the role at their next sign-in." });
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.groupMappings });
    },
  });
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="ghost" aria-label={`Remove mapping of ${mapping.group_id} to ${roleLabel}`}>
          <Trash2Icon aria-hidden="true" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove this group mapping?</DialogTitle>
          <DialogDescription>
            Members of <code className="font-mono">{mapping.group_id}</code> lose the {roleLabel} role at their next
            sign-in.
          </DialogDescription>
        </DialogHeader>
        {remove.isError && (
          <Alert variant="destructive">
            <AlertDescription>{actionErrorMessage(remove.error)}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={remove.isPending}>
              Cancel
            </Button>
          </DialogClose>
          <Button variant="destructive" onClick={() => remove.mutate()} disabled={remove.isPending}>
            {remove.isPending ? "Removing…" : "Remove mapping"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function GroupMappings({ canManage }: { canManage: boolean }) {
  const mappings = useGroupMappings();
  const roles = useRoles();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Entra ID group mappings</CardTitle>
        <CardDescription>Group → role mappings, applied at each sign-in with Microsoft.</CardDescription>
        {canManage && roles.data && (
          <CardAction>
            <AddMappingDialog roles={roles.data} />
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        {mappings.isPending ? (
          <RowsSkeleton label="Loading group mappings…" rows={3} />
        ) : mappings.isError && !mappings.data ? (
          <QueryError title="Could not load group mappings" error={mappings.error} onRetry={() => void mappings.refetch()} />
        ) : mappings.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No group mappings: single sign-on users get no roles.</p>
        ) : (
          <div className="rounded-lg border">
            <Table aria-label="Group mappings">
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">Provider</TableHead>
                  <TableHead scope="col">Group object ID</TableHead>
                  <TableHead scope="col">Role</TableHead>
                  {canManage && (
                    <TableHead scope="col">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  )}
                </TableRow>
              </TableHeader>
              <TableBody>
                {mappings.data.map((m) => {
                  const label = roleName(roles.data, m.role);
                  return (
                    <TableRow key={`${m.provider}/${m.group_id}/${m.role}`}>
                      <TableCell>{m.provider === ENTRA_PROVIDER ? "Microsoft Entra ID" : m.provider}</TableCell>
                      <TableCell className="font-mono text-xs break-all whitespace-normal">{m.group_id}</TableCell>
                      <TableCell>{label}</TableCell>
                      {canManage && (
                        <TableCell className="text-right">
                          <RemoveMappingButton mapping={m} roleLabel={label} />
                        </TableCell>
                      )}
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/** System → Users: users, roles, enable / disable and Entra ID group mappings. */
export function UsersView() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_USER_READ);
  const canManage = hasPermission(me, PERMISSION_USER_MANAGE);
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
        <p className="text-sm text-muted-foreground">
          Who can sign in and what they may do. Single sign-on users appear after their first sign-in.
        </p>
      </div>
      {canRead ? (
        <>
          <div className="space-y-2">
            <UsersTable canManage={canManage} />
            <RolesReference />
          </div>
          <GroupMappings canManage={canManage} />
        </>
      ) : (
        <AccessDenied what="Viewing users" permission={PERMISSION_USER_READ} />
      )}
    </div>
  );
}
