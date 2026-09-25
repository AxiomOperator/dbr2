// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func sample() *Manifest {
	t0 := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	u := uint32(999)
	return &Manifest{
		SchemaVersion: 1, RecoveryPointID: NewRecoveryPointID(t0), Status: StatusComplete, CreatedAt: t0.Add(time.Minute),
		ConsistencyMode: ModeQuiesced, ConsistencyPoint: t0, QuiesceStartedAt: &t0,
		Application: Application{ID: "app-1", Name: "shop", ComposeProject: "shop"},
		Source:      Source{HostID: "host-1", AgentID: "agent-1", Hostname: "h1"},
		Repository:  RepositoryRef{ID: "repo-1", Name: "primary"},
		Components: []Component{
			{Name: "config", Kind: KindConfig, Required: true, Status: ComponentSucceeded, SnapshotID: "k1", SnapshotSource: "agent@agent-1:/app-1/config", StartedAt: t0, FinishedAt: t0},
			{Name: "volume:data", Kind: KindVolume, Required: true, Status: ComponentSucceeded, SnapshotID: "k2", SnapshotSource: "agent@agent-1:/app-1/volume:data", StartedAt: t0, FinishedAt: t0, OwnerUID: &u, SELinuxContext: "system_u:object_r:container_file_t:s0"},
			{Name: "fsmeta:volume:data", Kind: KindFSMeta, Required: true, Parent: "volume:data", Status: ComponentSucceeded, SnapshotID: "k3", SnapshotSource: "agent@agent-1:/app-1/fsmeta:volume:data", StartedAt: t0, FinishedAt: t0},
			{Name: "image:web", Kind: KindImage, Required: false, Status: ComponentSucceeded, SnapshotID: "k4", SnapshotSource: "agent@agent-1:/app-1/image:web", StartedAt: t0, FinishedAt: t0},
		},
		Workflow: Workflow{WorkflowID: "application/app-1", RunID: "run"},
		Producer: Producer{Component: "worker", Version: "0.1.0.0"},
	}
}

func compile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(SchemaV1))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("v1.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundTripAndSchema(t *testing.T) {
	b, err := sample().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err := compile(t).Validate(inst); err != nil {
		t.Fatalf("schema: %v", err)
	}
	m, err := Parse(b)
	if err != nil || m.Components[1].SELinuxContext == "" || *m.Components[1].OwnerUID != 999 {
		t.Fatalf("parse = %+v, %v", m, err)
	}
	if err := m.ValidateSources("agent"); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaRejectsMissingSnapshot(t *testing.T) {
	m := sample()
	b, _ := json.Marshal(m)
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	delete(doc["components"].([]any)[0].(map[string]any), "snapshot_id")
	if err := compile(t).Validate(doc); err == nil {
		t.Fatal("schema accepted a succeeded component without snapshot_id")
	}
}

func TestInvariants(t *testing.T) {
	cases := map[string]func(*Manifest){
		"required failed":     func(m *Manifest) { m.Components[1].Status = ComponentFailed },
		"partial mismatch":    func(m *Manifest) { m.Status = StatusPartial },
		"complete w/ failure": func(m *Manifest) { m.Components[3].Status = ComponentFailed },
		"fsmeta missing": func(m *Manifest) {
			m.Components[2].Required = false
			m.Components[2].Status = ComponentFailed
			m.Status = StatusPartial
		},
		"fsmeta bad parent":   func(m *Manifest) { m.Components[2].Parent = "config" },
		"duplicate name":      func(m *Manifest) { m.Components[3].Name = "config" },
		"live not crash-only": func(m *Manifest) { m.ConsistencyMode = ModeLive },
		"bad rp id":           func(m *Manifest) { m.RecoveryPointID = "rp_x" },
	}
	for name, mut := range cases {
		m := sample()
		mut(m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	m := sample()
	m.Components[3].Status, m.Components[3].SnapshotID, m.Status = ComponentFailed, "", StatusPartial
	if err := m.Validate(); err != nil {
		t.Fatalf("partial: %v", err)
	}
}

func TestSourcesAndVersions(t *testing.T) {
	m := sample()
	m.Components[1].SnapshotSource = "agent@other-agent:/x"
	if err := m.ValidateSources("agent"); err == nil {
		t.Fatal("foreign source accepted")
	}
	if _, err := Parse([]byte(`{"schema_version":2}`)); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("v2: %v", err)
	}
	b, _ := sample().Marshal()
	b = []byte(strings.Replace(string(b), `"status"`, `"future_field": 1, "status"`, 1))
	if _, err := Parse(b); err != nil {
		t.Fatalf("additive field rejected: %v", err)
	}
}

func TestStatusFor(t *testing.T) {
	c := sample().Components
	if s, ok := StatusFor(c); !ok || s != StatusComplete {
		t.Fatal(s, ok)
	}
	c[3].Status = ComponentFailed
	if s, ok := StatusFor(c); !ok || s != StatusPartial {
		t.Fatal(s, ok)
	}
	c[0].Status = ComponentFailed
	if _, ok := StatusFor(c); ok {
		t.Fatal("required failure produced a recovery point")
	}
}
