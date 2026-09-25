// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
)

// Journal is the agent's local command journal (ADR-0001): an append-only
// JSON-lines file, fsynced on every state change, so a command is never
// executed twice for the same command_id and terminal results survive
// disconnects and restarts until the gateway acknowledges them.
type Journal struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	records map[string]*Record
	lines   int
}

// Record is the journaled state of one command.
type Record struct {
	CommandID string          `json:"command_id"`
	Kind      string          `json:"kind"`
	State     string          `json:"state"` // accepted | succeeded | failed | acked
	Updated   time.Time       `json:"updated"`
	Update    json.RawMessage `json:"update,omitempty"` // terminal CommandUpdate (protojson)
}

// Terminal reports whether the command finished.
func (r *Record) Terminal() bool { return r.State == "succeeded" || r.State == "failed" }

// OpenJournal loads (and compacts) the journal at path.
func OpenJournal(path string) (*Journal, error) {
	j := &Journal{path: path, records: map[string]*Record{}}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var r Record
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.CommandID != "" {
				rr := r
				j.records[r.CommandID] = &rr
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for id, r := range j.records { // acked commands no longer need to be kept
		if r.State == "acked" && time.Since(r.Updated) > 24*time.Hour {
			delete(j.records, id)
		}
	}
	if err := j.compact(); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *Journal) compact() error {
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, r := range j.records {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(tmp, j.path); err != nil {
		return err
	}
	if j.f != nil {
		j.f.Close()
	}
	j.f, err = os.OpenFile(j.path, os.O_APPEND|os.O_WRONLY, 0o600)
	j.lines = len(j.records)
	return err
}

func (j *Journal) write(r *Record) error {
	r.Updated = time.Now().UTC()
	j.records[r.CommandID] = r
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := j.f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := j.f.Sync(); err != nil {
		return err
	}
	j.lines++
	if j.lines > 1000 && j.lines > 4*len(j.records) {
		return j.compact()
	}
	return nil
}

// Get returns a copy of the record for id.
func (j *Journal) Get(id string) (Record, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, ok := j.records[id]
	if !ok {
		return Record{}, false
	}
	return *r, true
}

// Accept journals a new command before it starts.
func (j *Journal) Accept(id, kind string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.write(&Record{CommandID: id, Kind: kind, State: "accepted"})
}

// Finish journals a terminal update.
func (j *Journal) Finish(u *agentv1.CommandUpdate, kind string) error {
	b, err := protojson.Marshal(u)
	if err != nil {
		return err
	}
	state := "failed"
	if u.State == agentv1.CommandState_COMMAND_STATE_SUCCEEDED {
		state = "succeeded"
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.write(&Record{CommandID: u.CommandId, Kind: kind, State: state, Update: b})
}

// Ack marks a terminal result as persisted by the gateway.
func (j *Journal) Ack(id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, ok := j.records[id]
	if !ok || !r.Terminal() {
		return nil
	}
	// The result is kept so a re-dispatch of the same command_id (e.g. a
	// retried Temporal activity) still gets it.
	return j.write(&Record{CommandID: id, Kind: r.Kind, State: "acked", Update: r.Update})
}

// Result returns the stored terminal update for id (acked or not).
func (j *Journal) Result(id string) (*agentv1.CommandUpdate, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, ok := j.records[id]
	if !ok || len(r.Update) == 0 {
		return nil, false
	}
	var u agentv1.CommandUpdate
	if protojson.Unmarshal(r.Update, &u) != nil {
		return nil, false
	}
	return &u, true
}

// Unacked returns the terminal updates the gateway has not acknowledged.
func (j *Journal) Unacked() []*agentv1.CommandUpdate {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []*agentv1.CommandUpdate
	for _, r := range j.records {
		if !r.Terminal() {
			continue
		}
		var u agentv1.CommandUpdate
		if protojson.Unmarshal(r.Update, &u) == nil {
			out = append(out, &u)
		}
	}
	return out
}

// Counts summarizes the journal for `dbr2-agent status`.
func (j *Journal) Counts() map[string]int {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]int{}
	for _, r := range j.records {
		out[r.State]++
	}
	return out
}

// Close closes the journal file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f != nil {
		return j.f.Close()
	}
	return nil
}
