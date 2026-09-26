// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/store"
)

func app() fleet.Application {
	return fleet.Application{
		Record: store.ListApplicationsRow{ID: uuid.New(), AgentID: uuid.New(), Name: "shop"},
		Analysis: &inventory.Application{Name: "shop",
			Services:   []inventory.Service{{Name: "web", Containers: []inventory.ContainerRef{{ID: "c1", Name: "shop-web-1"}}}},
			Volumes:    []inventory.VolumeUse{{Name: "data", Mountpoint: "/v/data", Protected: true}, {Name: "cache", Mountpoint: "/v/cache", Protected: true}},
			BindMounts: []inventory.BindUse{{Source: "/srv/conf"}, {Source: "/var/run/docker.sock"}},
		},
	}
}

func rp(state string, t time.Time, status string, comps ...string) *store.RecoveryPoint {
	r := &store.RecoveryPoint{ID: "rp_x", State: state, CreatedAt: t, CommittedAt: &t, ConsistencyMode: "live"}
	if status != "" {
		r.Status = &status
		m := manifest.Manifest{Status: status}
		for _, c := range comps {
			m.Components = append(m.Components, manifest.Component{Name: c, Status: manifest.ComponentSucceeded, SizeBytes: 7})
		}
		r.Manifest, _ = json.Marshal(m)
	}
	return r
}

func TestProtectionStatuses(t *testing.T) {
	now := time.Now()
	all := []string{"config", "volume:data", "volume:cache", "bind:/srv/conf"}
	cases := []struct {
		name        string
		ok, attempt *store.RecoveryPoint
		excluded    []string
		want        string
		covered     int
	}{
		{"never", nil, nil, nil, StatusUnprotected, 0},
		{"only failures", nil, rp("failed", now, ""), nil, StatusFailed, 0},
		{"all covered", rp("committed", now, "complete", all...), rp("committed", now, "complete"), nil, StatusProtected, 4},
		{"new volume since", rp("committed", now, "complete", "config", "volume:data", "bind:/srv/conf"), nil, nil, StatusAtRisk, 3},
		{"excluded volume ok", rp("committed", now, "complete", "config", "volume:data", "bind:/srv/conf"), nil, []string{"volume:cache"}, StatusProtected, 3},
		{"partial", rp("committed", now, "partial", all...), nil, nil, StatusAtRisk, 4},
		{"later failure", rp("committed", now.Add(-time.Hour), "complete", all...), rp("failed", now, ""), nil, StatusFailed, 4},
	}
	for _, c := range cases {
		p := computeProtection(app(), BackupSettings{ExcludedComponents: c.excluded}, c.ok, c.attempt, false)
		if p.Status != c.want || p.ComponentsProtected != c.covered {
			t.Errorf("%s: status %s covered %d/%d (%v), want %s %d", c.name, p.Status, p.ComponentsProtected, p.ComponentsTotal, p.Reasons, c.want, c.covered)
		}
	}
	p := computeProtection(app(), BackupSettings{}, nil, rp("pending", now, ""), true)
	if p.Running != "restore" || p.ComponentsTotal != 4 { // the docker socket is never a component
		t.Fatalf("running/total: %+v", p)
	}
}
