// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"strings"

	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

// buildTopology records the application's containers, networks and volumes
// for the manifest (restore previews and collision checks; no secrets).
func buildTopology(x *inventory.Application, inv *inventory.Inventory) *manifest.Topology {
	t := &manifest.Topology{Containers: []manifest.TopologyContainer{}}
	byID := map[string]inventory.Container{}
	netByName := map[string]inventory.Network{}
	volByName := map[string]inventory.Volume{}
	if inv != nil {
		for _, c := range inv.Containers {
			byID[c.ID] = c
		}
		for _, n := range inv.Networks {
			netByName[n.Name] = n
		}
		for _, v := range inv.Volumes {
			volByName[v.Name] = v
		}
	}
	for _, svc := range x.Services {
		for _, ref := range svc.Containers {
			tc := manifest.TopologyContainer{ID: ref.ID, Name: strings.TrimPrefix(ref.Name, "/"), Service: svc.Name, Image: svc.Image, State: ref.State}
			if c, ok := byID[ref.ID]; ok {
				if c.Image != "" {
					tc.Image = c.Image
				}
				for _, p := range c.Ports {
					tc.Ports = append(tc.Ports, manifest.TopologyPort{ContainerPort: p.ContainerPort, Protocol: p.Protocol, HostIP: p.HostIP, HostPort: p.HostPort})
				}
				for _, m := range c.Mounts {
					tc.Mounts = append(tc.Mounts, manifest.TopologyMount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
				}
				tc.Networks = c.Networks
			}
			t.Containers = append(t.Containers, tc)
		}
	}
	for _, n := range x.Networks {
		tn := manifest.TopologyNetwork{Name: n.Name, Driver: n.Driver, External: n.External}
		if d, ok := netByName[n.Name]; ok {
			tn.Internal, tn.Attachable, tn.Labels = d.Internal, d.Attachable, d.Labels
		}
		t.Networks = append(t.Networks, tn)
	}
	for _, v := range x.Volumes {
		if v.Anonymous {
			continue
		}
		tv := manifest.TopologyVolume{Name: v.Name, Driver: v.Driver, External: v.External}
		if d, ok := volByName[v.Name]; ok {
			tv.Labels = d.Labels
		}
		t.Volumes = append(t.Volumes, tv)
	}
	return t
}
