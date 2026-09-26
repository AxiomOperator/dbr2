// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/engine/kopia"
)

// repoConnection is <state>/repositories/<id>/connection.json (0600). It
// holds the Kopia server user's password; never log it.
type repoConnection struct {
	RepositoryID string `json:"repository_id"`
	ServerURL    string `json:"server_url"`
	CertSHA256   string `json:"cert_sha256"`
	Username     string `json:"username"`
	Hostname     string `json:"hostname"`
	Password     string `json:"password"`
}

var repoIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var errRepoNotConfigured = errors.New("repository not configured")

func (a *Agent) repoDir(id string) string { return filepath.Join(a.cfg.StateDir, reposDir, id) }

func (a *Agent) loadConnection(id string) (*repoConnection, error) {
	if !repoIDRe.MatchString(id) {
		return nil, permanent(fmt.Errorf("invalid repository_id %q", id))
	}
	b, err := os.ReadFile(filepath.Join(a.repoDir(id), "connection.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, permanent(fmt.Errorf("%w: %s", errRepoNotConfigured, id))
	}
	if err != nil {
		return nil, err
	}
	var c repoConnection
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, permanent(fmt.Errorf("repository %s: corrupt connection.json: %w", id, err))
	}
	return &c, nil
}

// configureRepository persists the connection and verifies it by connecting.
func (a *Agent) configureRepository(ctx context.Context, c *agentv1.ConfigureRepositoryCommand) error {
	if !repoIDRe.MatchString(c.RepositoryId) {
		return permanent(fmt.Errorf("invalid repository_id %q", c.RepositoryId))
	}
	if c.ServerUrl == "" || c.Username == "" || c.Hostname == "" || c.Password == "" {
		return permanent(errors.New("server_url, username, hostname and password are required"))
	}
	conn := repoConnection{RepositoryID: c.RepositoryId, ServerURL: c.ServerUrl, CertSHA256: c.CertSha256,
		Username: c.Username, Hostname: c.Hostname, Password: c.Password}
	b, err := json.MarshalIndent(conn, "", "  ")
	if err != nil {
		return err
	}
	dir := a.repoDir(c.RepositoryId)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "connection.json"), b, 0o600); err != nil {
		return err
	}
	a.repos.invalidate(c.RepositoryId)
	h, err := a.openRepo(ctx, c.RepositoryId)
	if err != nil {
		return err
	}
	h.release()
	a.log.Info("repository configured", "repository_id", c.RepositoryId, "server", c.ServerUrl,
		"identity", c.Username+"@"+c.Hostname)
	return nil
}

// repoCache keeps one open session per repository id. Entries are
// reference-counted so a reconfigure or error never closes a session that
// another command is using.
type repoCache struct {
	mu      sync.Mutex
	entries map[string]*repoEntry
}

type repoEntry struct {
	id    string
	conn  *repoConnection
	repo  engine.Repository
	refs  int
	stale bool
	cache *repoCache
}

// repoHandle is a leased cache entry; call release when done.
type repoHandle struct{ *repoEntry }

func (h repoHandle) release() {
	c := h.cache
	c.mu.Lock()
	h.refs--
	closeIt := h.stale && h.refs == 0
	c.mu.Unlock()
	if closeIt {
		closeRepo(h.repo)
	}
}

// broken drops the session after an infrastructure error; the next command
// reconnects.
func (h repoHandle) broken() { h.cache.invalidate(h.id) }

func closeRepo(r engine.Repository) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = r.Close(ctx)
}

func (c *repoCache) invalidate(id string) {
	c.mu.Lock()
	e := c.entries[id]
	var closeIt bool
	if e != nil {
		delete(c.entries, id)
		e.stale = true
		closeIt = e.refs == 0
	}
	c.mu.Unlock()
	if closeIt {
		closeRepo(e.repo)
	}
}

func (c *repoCache) closeAll() {
	c.mu.Lock()
	ids := make([]string, 0, len(c.entries))
	for id := range c.entries {
		ids = append(ids, id)
	}
	c.mu.Unlock()
	for _, id := range ids {
		c.invalidate(id)
	}
}

// openRepo returns a cached session or connects. Not configured → permanent
// error; connection failures are retryable.
func (a *Agent) openRepo(ctx context.Context, id string) (repoHandle, error) {
	c := a.repos
	c.mu.Lock()
	if e := c.entries[id]; e != nil {
		e.refs++
		c.mu.Unlock()
		return repoHandle{e}, nil
	}
	c.mu.Unlock()
	conn, err := a.loadConnection(id)
	if err != nil {
		return repoHandle{}, err
	}
	r, err := a.connectRepo(ctx, id, conn)
	if err != nil {
		return repoHandle{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil { // lost a race: use the winner
		e.refs++
		go closeRepo(r)
		return repoHandle{e}, nil
	}
	e := &repoEntry{id: id, conn: conn, repo: r, refs: 1, cache: c}
	c.entries[id] = e
	return repoHandle{e}, nil
}

func (a *Agent) connectRepo(ctx context.Context, id string, conn *repoConnection) (engine.Repository, error) {
	open := a.OpenRepository
	if open == nil {
		open = kopia.ConnectServer
	}
	r, err := open(ctx, engine.ServerConnection{URL: conn.ServerURL, CertSHA256: conn.CertSHA256,
		User: conn.Username, Host: conn.Hostname, Password: conn.Password,
		StateDir: filepath.Join(a.repoDir(id), "kopia")})
	if err != nil {
		return nil, fmt.Errorf("repository %s: %w", id, err)
	}
	return r, nil
}

// repoSession returns a repository session and its release function. fresh
// opens a dedicated session closed on release (Kopia checks ACLs at session
// open, so a cached session may predate a cross-host READ grant).
func (a *Agent) repoSession(ctx context.Context, id string, fresh bool) (engine.Repository, func(), error) {
	if !fresh {
		h, err := a.openRepo(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		return h.repo, h.release, nil
	}
	conn, err := a.loadConnection(id)
	if err != nil {
		return nil, nil, err
	}
	r, err := a.connectRepo(ctx, id, conn)
	if err != nil {
		return nil, nil, err
	}
	return r, func() { closeRepo(r) }, nil
}
