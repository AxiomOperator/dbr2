// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/grpc/metadata"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/engine/kopia"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/repoclient"
)

// MaintUsername is the worker's Kopia identity (ADR-0004: the only
// identity allowed to write manifests and delete snapshots).
const MaintUsername = manifest.ManifestSourceUser + "@" + manifest.ManifestSourceHost

// MaintSessions keeps one maint@dbr2 session per Repository. Each new
// session sets a fresh random maint password on the reposerver first, so the
// password exists only in this process's memory (rotated on every start).
type MaintSessions struct {
	Platform controlv1.PlatformServiceClient
	Token    string
	// StateDir holds Kopia client config and cache (disposable).
	StateDir string
	// Connect and SetPassword are replaceable in tests.
	Connect     func(ctx context.Context, c engine.ServerConnection) (engine.Repository, error)
	SetPassword func(ctx context.Context, managementURL, username, password string) error

	mu   sync.Mutex
	open map[string]engine.Repository
}

// NewMaintSessions returns sessions backed by the reposerver.
func NewMaintSessions(p controlv1.PlatformServiceClient, token, stateDir string) *MaintSessions {
	return &MaintSessions{Platform: p, Token: token, StateDir: stateDir, Connect: kopia.ConnectServer,
		SetPassword: func(ctx context.Context, url, user, pw string) error {
			return repoclient.New(url, token).SetUser(ctx, user, pw)
		}}
}

// Get returns the open session for a Repository, connecting if needed.
func (m *MaintSessions) Get(ctx context.Context, repositoryID string) (engine.Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.open[repositoryID]; r != nil {
		return r, nil
	}
	resp, err := m.Platform.GetRepository(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+m.Token),
		&controlv1.GetRepositoryRequest{RepositoryId: repositoryID})
	if err != nil {
		return nil, platformErr(err)
	}
	repo := resp.Repository
	pw, err := randomPassword()
	if err != nil {
		return nil, err
	}
	if err := m.SetPassword(ctx, repo.ManagementUrl, MaintUsername, pw); err != nil {
		return nil, fmt.Errorf("set %s password on %s: %w", MaintUsername, repo.Name, err)
	}
	dir := filepath.Join(m.StateDir, "repositories", repositoryID)
	_ = os.RemoveAll(filepath.Join(dir, "kopia.config")) // reconnect with the new password
	u := repo.InternalServerUrl
	if u == "" {
		u = repo.ServerUrl
	}
	r, err := m.Connect(ctx, engine.ServerConnection{URL: u, CertSHA256: repo.CertSha256,
		User: manifest.ManifestSourceUser, Host: manifest.ManifestSourceHost, Password: pw, StateDir: dir})
	if err != nil {
		return nil, err
	}
	if m.open == nil {
		m.open = map[string]engine.Repository{}
	}
	m.open[repositoryID] = r
	return r, nil
}

// Invalidate drops a session after an error (the next Get reconnects).
func (m *MaintSessions) Invalidate(repositoryID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.open[repositoryID]; r != nil {
		_ = r.Close(context.Background())
		delete(m.open, repositoryID)
	}
}

// Close closes every session.
func (m *MaintSessions) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.open {
		_ = r.Close(context.Background())
		delete(m.open, id)
	}
}

func randomPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
