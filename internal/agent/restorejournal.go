// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Restore journal states.
const (
	restoreActive     = "active"
	restoreCommitted  = "committed"
	restoreRolledBack = "rolled_back"

	// Swap entry states, in order.
	swapStaging    = "staging"  // content is being written to Staging
	swapSwapping   = "swapping" // renames target→previous, staging→target in progress
	swapSwapped    = "swapped"
	swapRolledBack = "rolled_back"
	swapCommitted  = "committed"

	// finalizedKeep is how long finalized journals stay (idempotent repeats).
	finalizedKeep = 7 * 24 * time.Hour
)

// restoreJournal is <state>/restores/<restore_id>.json (0600, atomic
// writes). Every filesystem swap and every created volume, network and
// container is recorded BEFORE it happens, so FinalizeRestore can undo a
// restore after a crash or agent restart.
type restoreJournal struct {
	RestoreID         string             `json:"restore_id"`
	State             string             `json:"state"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	Swaps             []*swapRecord      `json:"swaps"`
	CreatedVolumes    []string           `json:"created_volumes,omitempty"`
	CreatedNetworks   []string           `json:"created_networks,omitempty"`
	CreatedContainers []createdContainer `json:"created_containers,omitempty"`
}

// swapRecord is one staged path (a component directory or a single file).
type swapRecord struct {
	Component string `json:"component"`
	// dir | file
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Staging  string `json:"staging"`
	Previous string `json:"previous"`
	Discard  string `json:"discard"`
	// The target existed (and was moved to Previous) when the swap began.
	HadPrevious bool   `json:"had_previous"`
	State       string `json:"state"`
}

type createdContainer struct {
	Name string `json:"name"`
	// Empty until the engine returned the ID (crash in between: by name).
	ID string `json:"id,omitempty"`
}

func (j *restoreJournal) ref(c createdContainer) string {
	if c.ID != "" {
		return c.ID
	}
	return c.Name
}

// restoreStore persists restore journals and serializes the commands of
// one restore.
type restoreStore struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newRestoreStore(dir string) *restoreStore {
	return &restoreStore{dir: dir, locks: map[string]*sync.Mutex{}}
}

// lock serializes the commands of one restore; call the returned func.
func (s *restoreStore) lock(id string) func() {
	s.mu.Lock()
	l := s.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *restoreStore) path(id string) string { return filepath.Join(s.dir, id+".json") }

// load returns the journal, or nil when there is none.
func (s *restoreStore) load(id string) (*restoreJournal, error) {
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j restoreJournal
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, permanent(fmt.Errorf("restore journal %s: %w", id, err))
	}
	return &j, nil
}

// open loads the journal or starts a new active one; a finalized restore
// cannot be changed.
func (s *restoreStore) open(id string) (*restoreJournal, error) {
	j, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		now := time.Now().UTC()
		return &restoreJournal{RestoreID: id, State: restoreActive, CreatedAt: now, Swaps: []*swapRecord{}}, nil
	}
	if j.State != restoreActive {
		return nil, permanent(fmt.Errorf("restore %s is already %s", id, strings.ReplaceAll(j.State, "_", " ")))
	}
	return j, nil
}

func (s *restoreStore) save(j *restoreJournal) error {
	j.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path(j.RestoreID), b, 0o600); err != nil {
		return fmt.Errorf("journal restore %s: %w", j.RestoreID, err)
	}
	return nil
}

// all loads every journal (unreadable ones are skipped).
func (s *restoreStore) all() []*restoreJournal {
	ents, _ := os.ReadDir(s.dir)
	var out []*restoreJournal
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || strings.HasPrefix(id, ".") {
			continue
		}
		if j, err := s.load(id); err == nil && j != nil {
			out = append(out, j)
		}
	}
	return out
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// undoSwap puts the previous content back (idempotent; works from any
// state the journal can hold after a crash). It reports whether a restored
// path was actually rolled back.
func undoSwap(r *swapRecord) (bool, error) {
	if r.State == swapRolledBack || r.State == swapCommitted {
		return false, nil
	}
	if err := os.RemoveAll(r.Staging); err != nil {
		return false, err
	}
	changed := false
	switch {
	case r.State == swapStaging:
		// Nothing was moved.
	case r.HadPrevious:
		// Previous exists only if the first rename happened.
		if exists(r.Previous) {
			if err := discard(r.Target, r.Discard); err != nil {
				return false, err
			}
			if err := os.Rename(r.Previous, r.Target); err != nil {
				return false, err
			}
			changed = true
		}
	default:
		if err := discard(r.Target, r.Discard); err != nil {
			return false, err
		}
		changed = true
	}
	_ = os.RemoveAll(r.Discard)
	r.State = swapRolledBack
	return changed, nil
}

// discard moves path aside (one rename, so the target never holds a
// half-deleted tree) and deletes it.
func discard(path, aside string) error {
	if !exists(path) {
		return nil
	}
	if err := os.RemoveAll(aside); err != nil {
		return err
	}
	if err := os.Rename(path, aside); err != nil {
		return err
	}
	return os.RemoveAll(aside)
}

// cleanupRestores runs at agent start: staging content of unfinalized
// restores that never reached the swap is removed (journals stay until
// FinalizeRestore), and finalized journals older than 7 days are dropped.
func (a *Agent) cleanupRestores() {
	for _, j := range a.restores.all() {
		if j.State != restoreActive {
			if time.Since(j.UpdatedAt) > finalizedKeep {
				_ = os.Remove(a.restores.path(j.RestoreID))
			}
			continue
		}
		changed := false
		for _, r := range j.Swaps {
			if r.State != swapStaging {
				continue
			}
			if err := os.RemoveAll(r.Staging); err != nil {
				a.log.Warn("remove restore staging failed", "restore_id", j.RestoreID, "path", r.Staging, "err", err)
				continue
			}
			r.State, changed = swapRolledBack, true
		}
		if changed {
			if err := a.restores.save(j); err != nil {
				a.log.Error("journal restore failed", "restore_id", j.RestoreID, "err", err)
			}
			a.log.Info("removed staging of an interrupted restore", "restore_id", j.RestoreID)
		}
	}
}
