// SPDX-License-Identifier: Apache-2.0

// Package reposerver implements dbr2-reposerver's Kopia repository server
// (ADR-0002, ADR-0007): it creates the Kopia filesystem repository on the
// guarded storage path, holds the repository password, supervises the
// in-process-CLI Kopia server child, and manages Kopia server users and ACLs
// through an internal management API used by dbr2-server and dbr2-worker.
package reposerver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Kopia identities (ADR-0002).
const (
	// ServerUser/ServerHost is the reposerver's own direct-connection
	// identity; it creates the repository and is therefore the Kopia
	// maintenance owner (the server runs quick/full maintenance itself).
	ServerUser = "reposerver"
	ServerHost = "dbr2"
	// MaintUser is the DBR² maintenance identity used by dbr2-worker.
	MaintUser = "maint@dbr2"
	// ControlUser is the Kopia server control-API user (`server refresh`).
	ControlUser = "control"
	// DefaultSplitter is the object splitter for new DBR² Repositories.
	DefaultSplitter = "DYNAMIC-1M-BUZHASH"
	// GlobalCompression is the global Kopia compression policy.
	GlobalCompression = "zstd-fastest"
	// KeepForever is used for every Kopia retention keep-* setting: DBR²
	// deletes whole recovery points itself (through maint@dbr2) and pins
	// every snapshot, so Kopia's own retention must never delete anything.
	KeepForever = 1_000_000_000
	// formatBlob is Kopia's repository format blob in filesystem storage.
	formatBlob = "kopia.repository.f"
)

// Errors mapped to API problem codes.
var (
	ErrAlreadyInitialized = errors.New("repository already initialized")
	ErrNotInitialized     = errors.New("repository not initialized")
	ErrStorageNotReady    = errors.New("repository storage not ready")
	ErrNotFound           = errors.New("not found")
	ErrInvalidPassword    = errors.New("the storage already holds a Kopia repository and the password does not open it")
)

// Status is the GET /v1/status document (management API contract).
type Status struct {
	RepositoryID      string `json:"repository_id"`
	Initialized       bool   `json:"initialized"`
	ServerRunning     bool   `json:"server_running"`
	KopiaAddress      string `json:"kopia_address"`
	CertSHA256        string `json:"cert_sha256"`
	KopiaVersion      string `json:"kopia_version"`
	Splitter          string `json:"splitter"`
	StoragePath       string `json:"storage_path"`
	StorageHealthy    bool   `json:"storage_healthy"`
	StorageError      string `json:"storage_error"`
	StorageTotalBytes uint64 `json:"storage_total_bytes"`
	StorageFreeBytes  uint64 `json:"storage_free_bytes"`
	StorageUsedBytes  uint64 `json:"storage_used_bytes"`
}

// ServerProcess is the supervised Kopia server.
type ServerProcess interface {
	Start(ctx context.Context)
	Running() bool
}

// Service owns the Repository: initialization, users and ACLs. All
// mutating operations are serialized.
type Service struct {
	RepositoryID string
	StoragePath  string
	KopiaAddr    string
	State        State
	Runner       Runner
	Server       ServerProcess
	// GuardCheck runs the storage guard (bounded by the watchdog timeout).
	GuardCheck func() error
	// StorageHealth is the watchdog's last observation.
	StorageHealth   func() (bool, string)
	CertSHA256      string
	ControlPassword string
	Log             *slog.Logger
	// Lifetime is the context the Kopia server is started under.
	Lifetime context.Context

	mu       sync.Mutex
	password atomic.Pointer[string]
	info     atomic.Pointer[RepoInfo]
}

// Load restores the persisted state at start-up. It reports whether the
// Repository is initialized.
func (s *Service) Load() (bool, error) {
	ri, err := s.State.LoadInfo()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pw, err := ReadSecret(s.State.PasswordFile())
	if err != nil || pw == "" {
		return false, fmt.Errorf("repository is initialized but %s is unreadable: %v", s.State.PasswordFile(), err)
	}
	if _, err := os.Stat(s.State.ConfigFile()); err != nil {
		return false, fmt.Errorf("repository is initialized but the Kopia config is missing: %w", err)
	}
	s.password.Store(&pw)
	s.info.Store(ri)
	return true, nil
}

// Password returns the repository password ("" before initialization).
func (s *Service) Password() string {
	if p := s.password.Load(); p != nil {
		return *p
	}
	return ""
}

// Initialized reports whether initialize completed.
func (s *Service) Initialized() bool { return s.info.Load() != nil }

// ServerInvocation is the `server start` command for the supervisor.
func (s *Service) ServerInvocation() (Invocation, string, error) {
	pw := s.Password()
	if pw == "" {
		return Invocation{}, "", ErrNotInitialized
	}
	return Invocation{
		Args: []string{"server", "start",
			"--address=https://" + s.KopiaAddr,
			"--no-ui",
			"--tls-cert-file=" + s.State.CertFile(),
			"--tls-key-file=" + s.State.KeyFile(),
			"--server-control-username=" + ControlUser,
			"--no-persistent-logs",
			"--shutdown-grace-period=15s",
		},
		Env:     map[string]string{"KOPIA_SERVER_CONTROL_PASSWORD": s.ControlPassword},
		Secrets: []string{s.ControlPassword},
	}, pw, nil
}

// Status reports the Repository state and storage usage.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{
		RepositoryID:  s.RepositoryID,
		Initialized:   s.Initialized(),
		ServerRunning: s.Server != nil && s.Server.Running(),
		KopiaAddress:  s.KopiaAddr,
		CertSHA256:    s.CertSHA256,
		KopiaVersion:  KopiaVersion(),
		StoragePath:   s.StoragePath,
	}
	if ri := s.info.Load(); ri != nil {
		st.Splitter = ri.Splitter
	}
	if s.StorageHealth != nil {
		st.StorageHealthy, st.StorageError = s.StorageHealth()
	}
	total, free, used, err := statfs(ctx, s.StoragePath, 3*time.Second)
	if err != nil {
		st.StorageHealthy = false
		if st.StorageError == "" {
			st.StorageError = err.Error()
		}
	} else {
		st.StorageTotalBytes, st.StorageFreeBytes, st.StorageUsedBytes = total, free, used
	}
	return st
}

func statfs(ctx context.Context, path string, timeout time.Duration) (total, free, used uint64, err error) {
	type res struct {
		st  syscall.Statfs_t
		err error
	}
	ch := make(chan res, 1) // a stalled hard NFS mount blocks statfs: never wait for it unbounded
	go func() {
		var r res
		r.err = syscall.Statfs(path, &r.st)
		ch <- r
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return 0, 0, 0, r.err
		}
		bs := uint64(r.st.Bsize) //nolint:gosec // block size is positive
		return r.st.Blocks * bs, r.st.Bavail * bs, (r.st.Blocks - r.st.Bfree) * bs, nil
	case <-time.After(timeout):
		return 0, 0, 0, errors.New("statfs timed out (stalled mount?)")
	case <-ctx.Done():
		return 0, 0, 0, ctx.Err()
	}
}

func (s *Service) guard() error {
	if err := s.GuardCheck(); err != nil {
		return fmt.Errorf("%w: %v", ErrStorageNotReady, err)
	}
	return nil
}

// ready gates every operation on an initialized Repository.
func (s *Service) ready() error {
	if !s.Initialized() {
		return ErrNotInitialized
	}
	return s.guard()
}

// Initialize creates the Kopia repository on the guarded path (or
// re-attaches to one this reposerver lost its state for), sets the global
// policy and the DBR² ACL set, persists the password and starts the server.
func (s *Service) Initialize(ctx context.Context, password, splitter string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Initialized() {
		return Status{}, ErrAlreadyInitialized
	}
	if err := s.guard(); err != nil {
		return Status{}, err
	}
	if splitter == "" {
		splitter = DefaultSplitter
	}
	// A stale config from an interrupted attempt would make create/connect fail.
	if err := os.Remove(s.State.ConfigFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	s.password.Store(&password)
	ok := false
	defer func() {
		if !ok {
			s.password.Store(nil)
		}
	}()

	_, statErr := os.Stat(filepath.Join(s.StoragePath, formatBlob))
	exists := statErr == nil
	common := []string{
		"--path=" + s.StoragePath,
		"--cache-directory=" + s.State.CacheDir(),
		"--override-username=" + ServerUser,
		"--override-hostname=" + ServerHost,
		"--no-check-for-updates",
		"--description=DBR² repository " + s.RepositoryID,
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if exists {
		// Re-attach (e.g. the state volume was lost and the escrowed
		// password is supplied again): connect, then reconcile policy/ACLs.
		s.Log.Warn("storage already holds a Kopia repository; connecting to it", "path", s.StoragePath)
		if _, err := s.Runner.Run(cctx, Invocation{Args: append([]string{"repository", "connect", "filesystem"}, common...)}); err != nil {
			if strings.Contains(err.Error(), "invalid repository password") {
				return Status{}, ErrInvalidPassword
			}
			return Status{}, err
		}
	} else {
		args := append([]string{"repository", "create", "filesystem"}, common...)
		args = append(args, "--object-splitter="+splitter)
		if _, err := s.Runner.Run(cctx, Invocation{Args: args}); err != nil {
			return Status{}, err
		}
	}
	// Persist the password immediately: from here on the storage holds a
	// repository that only this password (and the escrow package) opens.
	if err := writeFileAtomic(s.State.PasswordFile(), []byte(password+"\n"), 0o600); err != nil {
		return Status{}, fmt.Errorf("write repository password: %w", err)
	}
	actual, err := s.repoSplitter(cctx)
	if err != nil {
		return Status{}, err
	}
	if err := s.setGlobalPolicy(cctx); err != nil {
		return Status{}, err
	}
	if err := s.reconcileACLs(cctx); err != nil {
		return Status{}, err
	}
	ri := &RepoInfo{RepositoryID: s.RepositoryID, Splitter: actual, InitializedAt: time.Now().UTC()}
	if err := s.State.SaveInfo(ri); err != nil {
		return Status{}, err
	}
	s.info.Store(ri)
	ok = true
	s.Log.Info("repository initialized", "repository_id", s.RepositoryID, "splitter", actual, "reattached", exists)
	if s.Server != nil {
		s.Server.Start(s.Lifetime)
		// Give the server a moment so the returned status is useful.
		for deadline := time.Now().Add(20 * time.Second); !s.Server.Running() && time.Now().Before(deadline) && ctx.Err() == nil; {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return s.Status(ctx), nil
}

// repoSplitter reads the object splitter from the repository format.
func (s *Service) repoSplitter(ctx context.Context) (string, error) {
	out, err := s.Runner.Run(ctx, Invocation{Args: []string{"repository", "status", "--json"}})
	if err != nil {
		return "", err
	}
	var st struct {
		ObjectFormat struct {
			Splitter string `json:"splitter"`
		} `json:"objectFormat"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", fmt.Errorf("parse repository status: %w", err)
	}
	return st.ObjectFormat.Splitter, nil
}

// GlobalPolicyArgs is the global policy DBR² sets: zstd-fastest compression
// (the reposerver compresses for every agent, and database dumps and
// images arrive already zstd-compressed by the agent, so the cheapest zstd
// level keeps most of the gain for a fraction of the CPU), retention that
// never deletes. Kopia's global policy defines no snapshot schedule (and
// Kopia refuses --manual on the global policy), so the server never takes
// snapshots of its own.
func GlobalPolicyArgs() []string {
	n := strconv.Itoa(KeepForever)
	return []string{"policy", "set", "--global",
		"--compression=" + GlobalCompression,
		"--keep-latest=" + n, "--keep-hourly=" + n, "--keep-daily=" + n,
		"--keep-weekly=" + n, "--keep-monthly=" + n, "--keep-annual=" + n,
	}
}

func (s *Service) setGlobalPolicy(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, Invocation{Args: GlobalPolicyArgs()})
	return err
}

// ACL is one Kopia server ACL entry (`server acl list --json`).
type ACL struct {
	ID     string            `json:"id"`
	User   string            `json:"user"`
	Access string            `json:"access"`
	Target map[string]string `json:"target"`
}

// TargetString renders a target the way `server acl add --target` takes it.
func TargetString(t map[string]string) string {
	keys := slices.Sorted(maps.Keys(t))
	slices.SortStableFunc(keys, func(a, b string) int { // "type" first, for readability
		switch {
		case a == "type" && b != "type":
			return -1
		case b == "type" && a != "type":
			return 1
		}
		return 0
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+t[k])
	}
	return strings.Join(parts, ",")
}

// DesiredACLs is the spike-verified DBR² ACL set (ADR-0002;
// spikes/kopia-library/RESULTS.md Q2). Kopia's `server acl enable` defaults
// give every user FULL on its own snapshots, policies and user record; those
// are replaced.
var DesiredACLs = []ACL{
	{User: "*@*", Access: "APPEND", Target: map[string]string{"type": "content"}},
	{User: "*@*", Access: "READ", Target: map[string]string{"type": "policy", "policyType": "global"}},
	{User: "*@*", Access: "READ", Target: map[string]string{"type": "policy", "policyType": "host", "hostname": "OWN_HOST"}},
	{User: "*@*", Access: "APPEND", Target: map[string]string{"type": "snapshot", "username": "OWN_USER", "hostname": "OWN_HOST"}},
	{User: "*@*", Access: "READ", Target: map[string]string{"type": "policy", "username": "OWN_USER", "hostname": "OWN_HOST"}},
	{User: MaintUser, Access: "FULL", Target: map[string]string{"type": "snapshot"}},
	{User: MaintUser, Access: "FULL", Target: map[string]string{"type": "policy"}},
}

func sameEntry(a, b ACL) bool {
	return a.User == b.User && a.Access == b.Access && maps.Equal(a.Target, b.Target)
}

// PlanACLs computes the changes that turn current into DesiredACLs while
// keeping entries DBR² added later (per-user read grants): every wildcard
// (*@*) entry and every maint@dbr2 entry not in the desired set is removed.
func PlanACLs(current []ACL) (del []string, add []ACL) {
	for _, e := range current {
		if e.User != "*@*" && e.User != MaintUser {
			continue
		}
		if !slices.ContainsFunc(DesiredACLs, func(d ACL) bool { return sameEntry(d, e) }) {
			del = append(del, e.ID)
		}
	}
	for _, d := range DesiredACLs {
		if !slices.ContainsFunc(current, func(e ACL) bool { return sameEntry(d, e) }) {
			add = append(add, d)
		}
	}
	return del, add
}

func (s *Service) listACLs(ctx context.Context) ([]ACL, error) {
	out, err := s.Runner.Run(ctx, Invocation{Args: []string{"server", "acl", "list", "--json"}})
	if err != nil {
		return nil, err
	}
	var acls []ACL
	if err := json.Unmarshal(out, &acls); err != nil {
		return nil, fmt.Errorf("parse ACL list: %w", err)
	}
	return acls, nil
}

func (s *Service) addACL(ctx context.Context, a ACL) error {
	_, err := s.Runner.Run(ctx, Invocation{Args: []string{"server", "acl", "add",
		"--user=" + a.User, "--access=" + a.Access, "--target=" + TargetString(a.Target)}})
	return err
}

func (s *Service) deleteACLs(ctx context.Context, ids ...string) error {
	_, err := s.Runner.Run(ctx, Invocation{Args: append([]string{"server", "acl", "delete", "--delete"}, ids...)})
	return err
}

func (s *Service) reconcileACLs(ctx context.Context) error {
	cur, err := s.listACLs(ctx)
	if err != nil {
		return err
	}
	if len(cur) == 0 {
		// Enables ACL enforcement (an empty ACL set means the legacy
		// allow-all authorizer) and installs Kopia's defaults.
		if _, err := s.Runner.Run(ctx, Invocation{Args: []string{"server", "acl", "enable"}}); err != nil {
			return err
		}
		if cur, err = s.listACLs(ctx); err != nil {
			return err
		}
	}
	del, add := PlanACLs(cur)
	// Delete first: Kopia allows one entry per user+target, so a FULL
	// default must be gone before its APPEND/READ replacement is added.
	// The server is not running while this happens (initialize only).
	if len(del) > 0 {
		if err := s.deleteACLs(ctx, del...); err != nil {
			return err
		}
	}
	for _, a := range add {
		if err := s.addACL(ctx, a); err != nil {
			return err
		}
	}
	s.Log.Info("server ACLs reconciled", "added", len(add), "removed", len(del))
	return nil
}

// refresh makes the running server reload users and ACLs.
func (s *Service) refresh(ctx context.Context) error {
	if s.Server == nil || !s.Server.Running() {
		return nil // the server loads users and ACLs when it starts
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = s.Runner.Run(ctx, Invocation{
			Args: []string{"server", "refresh",
				"--address=https://" + loopback(s.KopiaAddr),
				"--server-cert-fingerprint=" + s.CertSHA256,
				"--server-control-username=" + ControlUser},
			Env:     map[string]string{"KOPIA_SERVER_PASSWORD": s.ControlPassword},
			Secrets: []string{s.ControlPassword},
		})
		if err == nil || ctx.Err() != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("server refresh: %w", err)
	}
	return nil
}

func (s *Service) users(ctx context.Context) ([]string, error) {
	out, err := s.Runner.Run(ctx, Invocation{Args: []string{"server", "user", "list"}})
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// PutUser creates or updates a Kopia server user.
func (s *Service) PutUser(ctx context.Context, username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	users, err := s.users(ctx)
	if err != nil {
		return err
	}
	verb := "add"
	if slices.Contains(users, username) {
		verb = "set"
	}
	if _, err := s.Runner.Run(ctx, Invocation{
		Args:       []string{"server", "user", verb, username},
		SecretArgs: []string{"--user-password=" + password},
	}); err != nil {
		return err
	}
	s.Log.Info("kopia server user saved", "user", username, "created", verb == "add")
	return s.refresh(ctx)
}

// DeleteUser removes a Kopia server user.
func (s *Service) DeleteUser(ctx context.Context, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	users, err := s.users(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(users, username) {
		return ErrNotFound
	}
	if _, err := s.Runner.Run(ctx, Invocation{Args: []string{"server", "user", "delete", username}}); err != nil {
		return err
	}
	s.Log.Info("kopia server user deleted", "user", username)
	return s.refresh(ctx)
}

func readGrant(user, srcUser, srcHost string) ACL {
	return ACL{User: user, Access: "READ", Target: map[string]string{"type": "snapshot", "username": srcUser, "hostname": srcHost}}
}

// isReadGrant reports whether e has the shape AddReadGrant creates, so the
// delete endpoint can never remove the base ACL set.
func isReadGrant(e ACL) bool {
	return e.Access == "READ" && e.User != "*@*" && !strings.Contains(e.User, "*") && len(e.Target) == 3 &&
		e.Target["type"] == "snapshot" && e.Target["username"] != "" && e.Target["hostname"] != "" &&
		e.Target["username"] != "OWN_USER" && e.Target["hostname"] != "OWN_HOST"
}

// AddReadGrant gives user READ on the source user@host's snapshots
// (cross-host restore). An identical existing grant is returned as is.
func (s *Service) AddReadGrant(ctx context.Context, user, srcUser, srcHost string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return "", err
	}
	want := readGrant(user, srcUser, srcHost)
	find := func() (string, error) {
		cur, err := s.listACLs(ctx)
		if err != nil {
			return "", err
		}
		for _, e := range cur {
			if sameEntry(e, want) {
				return e.ID, nil
			}
		}
		return "", nil
	}
	id, err := find()
	if err != nil || id != "" {
		return id, err
	}
	if err := s.addACL(ctx, want); err != nil {
		return "", err
	}
	if id, err = find(); err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("read grant was added but is not listed")
	}
	s.Log.Info("read grant added", "id", id, "user", user, "source", srcUser+"@"+srcHost)
	return id, s.refresh(ctx)
}

// DeleteReadGrant revokes a grant created by AddReadGrant. Kopia evaluates
// ACLs when a client session starts, so open sessions keep the access they
// had until they reconnect.
func (s *Service) DeleteReadGrant(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	cur, err := s.listACLs(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(cur, func(e ACL) bool { return e.ID == id })
	if i < 0 || !isReadGrant(cur[i]) {
		return ErrNotFound
	}
	if err := s.deleteACLs(ctx, id); err != nil {
		return err
	}
	s.Log.Info("read grant revoked", "id", id, "user", cur[i].User)
	return s.refresh(ctx)
}

// loopback maps a listen address to a dialable loopback address.
func loopback(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
