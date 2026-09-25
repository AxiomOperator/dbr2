// SPDX-License-Identifier: Apache-2.0

// Package manifest defines the DBR² recovery manifest (component
// `manifest-schema`, ADR-0003, ADR-0004). A recovery point exists if and only
// if its manifest exists in the Repository, written by maint@dbr2. The JSON
// Schema is published as schema/v1.json; readers must accept every earlier
// schema version, and changes within a major version are additive.
package manifest

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// SchemaVersion is the current manifest schema version.
const SchemaVersion = 1

// SchemaV1 is the JSON Schema (draft 2020-12) for schema_version 1.
//
//go:embed schema/v1.json
var SchemaV1 []byte

// ManifestSourceUser and ManifestSourceHost form the only identity trusted
// to write manifests (ADR-0004): source maint@dbr2:/manifests/<app_id>.
const (
	ManifestSourceUser = "maint"
	ManifestSourceHost = "dbr2"
	ManifestFileName   = "manifest.json"
)

// ManifestPath is the snapshot source path for an application's manifests.
func ManifestPath(applicationID string) string { return "/manifests/" + applicationID }

// Component kinds (ADR-0004).
const (
	KindConfig    = "config"
	KindVolume    = "volume"
	KindBindMount = "bind_mount"
	KindDatabase  = "database"
	KindImage     = "image"
	KindFSMeta    = "fsmeta"
)

// Recovery point statuses.
const (
	StatusComplete = "complete"
	StatusPartial  = "partial"
)

// Component statuses.
const (
	ComponentSucceeded = "succeeded"
	ComponentFailed    = "failed"
	ComponentSkipped   = "skipped"
)

// Consistency modes (ADR-0005).
const (
	ModeLive     = "live"
	ModeQuiesced = "quiesced"
	ModeOffline  = "offline"
)

// Manifest is the recovery manifest document.
type Manifest struct {
	SchemaVersion       int           `json:"schema_version"`
	RecoveryPointID     string        `json:"recovery_point_id"`
	Status              string        `json:"status"`
	CreatedAt           time.Time     `json:"created_at"`
	ConsistencyMode     string        `json:"consistency_mode"`
	ConsistencyPoint    time.Time     `json:"consistency_point"`
	CrashConsistentOnly bool          `json:"crash_consistent_only"`
	QuiesceStartedAt    *time.Time    `json:"quiesce_started_at,omitempty"`
	QuiesceEndedAt      *time.Time    `json:"quiesce_ended_at,omitempty"`
	AutoResumed         bool          `json:"auto_resumed,omitempty"`
	Application         Application   `json:"application"`
	Source              Source        `json:"source"`
	Repository          RepositoryRef `json:"repository"`
	Components          []Component   `json:"components"`
	Images              []Image       `json:"images,omitempty"`
	Contract            *Contract     `json:"contract,omitempty"`
	Workflow            Workflow      `json:"workflow"`
	Producer            Producer      `json:"producer"`
}

// Application identifies the protected application.
type Application struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ComposeProject string `json:"compose_project,omitempty"`
	WorkingDir     string `json:"working_dir,omitempty"`
}

// Source is the host and runtime the recovery point was captured from.
type Source struct {
	HostID         string `json:"host_id"`
	AgentID        string `json:"agent_id"`
	Hostname       string `json:"hostname"`
	OSRelease      string `json:"os_release,omitempty"`
	Architecture   string `json:"architecture,omitempty"`
	RuntimeVersion string `json:"runtime_version,omitempty"`
	AgentVersion   string `json:"agent_version,omitempty"`
}

// RepositoryRef names the Repository holding the recovery point.
type RepositoryRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Component is one captured component.
type Component struct {
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Required       bool      `json:"required"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	SnapshotID     string    `json:"snapshot_id,omitempty"`
	RootObjectID   string    `json:"root_object_id,omitempty"`
	SnapshotSource string    `json:"snapshot_source,omitempty"`
	SizeBytes      int64     `json:"size_bytes"`
	Files          int64     `json:"files,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	Path           string    `json:"path,omitempty"`
	VolumeName     string    `json:"volume_name,omitempty"`
	OwnerUID       *uint32   `json:"owner_uid,omitempty"`
	OwnerGID       *uint32   `json:"owner_gid,omitempty"`
	Mode           string    `json:"mode,omitempty"`
	SELinuxContext string    `json:"selinux_context,omitempty"`
	Parent         string    `json:"parent,omitempty"`
	CaptureMethod  string    `json:"capture_method,omitempty"`
}

// Image is an image reference used by the application.
type Image struct {
	Service string `json:"service,omitempty"`
	Ref     string `json:"ref"`
	Digest  string `json:"digest,omitempty"`
}

// Contract is the recovery-contract evaluation at capture time (Phase 7+).
type Contract struct {
	ID        string          `json:"id,omitempty"`
	Satisfied bool            `json:"satisfied"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// Workflow identifies the workflow run that produced the recovery point.
type Workflow struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	Trigger    string `json:"trigger,omitempty"`
}

// Producer identifies the DBR² build that wrote the manifest.
type Producer struct {
	Component string `json:"component"`
	Version   string `json:"version"`
}

var (
	rpIDRe   = regexp.MustCompile(`^rp_[0-9A-HJKMNP-TV-Z]{26}$`)
	kinds    = map[string]bool{KindConfig: true, KindVolume: true, KindBindMount: true, KindDatabase: true, KindImage: true, KindFSMeta: true}
	modes    = map[string]bool{ModeLive: true, ModeQuiesced: true, ModeOffline: true}
	statuses = map[string]bool{ComponentSucceeded: true, ComponentFailed: true, ComponentSkipped: true}
)

// ErrUnsupportedVersion is returned for manifests from a newer major schema.
var ErrUnsupportedVersion = errors.New("manifest: unsupported schema_version")

// Parse decodes and validates a manifest. Unknown fields are accepted
// (additive changes within a major version).
func Parse(b []byte) (*Manifest, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if probe.SchemaVersion < 1 || probe.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("%w %d", ErrUnsupportedVersion, probe.SchemaVersion)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Marshal validates and encodes a manifest.
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}

// Validate checks the invariants of ADR-0004 that the JSON Schema cannot
// express: the status matches the component outcomes, fsmeta parents exist,
// and names are unique.
func (m *Manifest) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if m.SchemaVersion != SchemaVersion {
		add("schema_version must be %d", SchemaVersion)
	}
	if !rpIDRe.MatchString(m.RecoveryPointID) {
		add("recovery_point_id %q is not rp_<ULID>", m.RecoveryPointID)
	}
	if !modes[m.ConsistencyMode] {
		add("consistency_mode %q is invalid", m.ConsistencyMode)
	}
	if m.ConsistencyMode == ModeLive && !m.CrashConsistentOnly {
		add("live recovery points must be crash_consistent_only")
	}
	if m.Application.ID == "" || m.Source.AgentID == "" || m.Source.HostID == "" || m.Repository.ID == "" {
		add("application.id, source.host_id, source.agent_id and repository.id are required")
	}
	if m.Workflow.WorkflowID == "" || m.Workflow.RunID == "" {
		add("workflow.workflow_id and workflow.run_id are required")
	}
	if m.CreatedAt.IsZero() || m.ConsistencyPoint.IsZero() {
		add("created_at and consistency_point are required")
	}
	if len(m.Components) == 0 {
		add("at least one component is required")
	}
	names := map[string]*Component{}
	failedOptional := false
	for i := range m.Components {
		c := &m.Components[i]
		if c.Name == "" || names[c.Name] != nil {
			add("component %d: name %q is empty or duplicated", i, c.Name)
		}
		names[c.Name] = c
		if !kinds[c.Kind] {
			add("component %s: kind %q is invalid", c.Name, c.Kind)
		}
		if !statuses[c.Status] {
			add("component %s: status %q is invalid", c.Name, c.Status)
		}
		switch {
		case c.Status == ComponentSucceeded && (c.SnapshotID == "" || c.SnapshotSource == ""):
			add("component %s: succeeded without snapshot_id and snapshot_source", c.Name)
		case c.Status != ComponentSucceeded && c.Required:
			add("component %s: required component did not succeed (no recovery point may exist)", c.Name)
		case c.Status != ComponentSucceeded:
			failedOptional = true
		}
	}
	for _, c := range m.Components {
		if c.Kind == KindFSMeta {
			p := names[c.Parent]
			if p == nil || (p.Kind != KindVolume && p.Kind != KindBindMount) {
				add("fsmeta component %s: parent %q is not a filesystem component", c.Name, c.Parent)
			} else if p.Status == ComponentSucceeded && c.Status != ComponentSucceeded {
				add("fsmeta component %s: required whenever %s is present", c.Name, p.Name)
			}
		}
	}
	switch {
	case m.Status == StatusComplete && failedOptional:
		add("status complete but optional components failed; must be partial")
	case m.Status == StatusPartial && !failedOptional:
		add("status partial but every component succeeded; must be complete")
	case m.Status != StatusComplete && m.Status != StatusPartial:
		add("status %q is invalid", m.Status)
	}
	if len(errs) > 0 {
		return fmt.Errorf("manifest: invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ValidateSources checks that every succeeded component was written by the
// recovery point's agent (ADR-0004 amendment): snapshot_source must be
// <agentUser>@<agent_id>:<path>. Reindexing and commit both call it.
func (m *Manifest) ValidateSources(agentUser string) error {
	want := agentUser + "@" + m.Source.AgentID + ":"
	for _, c := range m.Components {
		if c.Status == ComponentSucceeded && !strings.HasPrefix(c.SnapshotSource, want) {
			return fmt.Errorf("manifest: component %s source %q was not written by agent %s", c.Name, c.SnapshotSource, m.Source.AgentID)
		}
	}
	return nil
}

// StatusFor derives the recovery point status from component outcomes.
// ok is false when a required component did not succeed (no recovery point).
func StatusFor(components []Component) (status string, ok bool) {
	status = StatusComplete
	for _, c := range components {
		if c.Status == ComponentSucceeded {
			continue
		}
		if c.Required {
			return "", false
		}
		status = StatusPartial
	}
	return status, true
}

// NewRecoveryPointID returns a new rp_<ULID> identifier.
func NewRecoveryPointID(now time.Time) string {
	return "rp_" + ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
}
