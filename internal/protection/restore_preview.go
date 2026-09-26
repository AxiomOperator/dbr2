// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"fmt"
	"path"
	"sort"
	"strings"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

// PathRemap rewrites a host path prefix for bind mounts and config files.
type PathRemap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Remap applies the first matching remap (whole path components only).
func Remap(p string, remaps []PathRemap) string {
	for _, r := range remaps {
		from := strings.TrimRight(r.From, "/")
		if p == from || strings.HasPrefix(p, from+"/") {
			return path.Clean(strings.TrimRight(r.To, "/") + strings.TrimPrefix(p, from))
		}
	}
	return p
}

// AgentRoleLabel marks a containerized dbr2-agent (value "agent"). It
// mounts host paths to read and restore them, so its mounts are not
// collisions.
const AgentRoleLabel = "dbr2.role"

// Preview is the restore impact preview (ADR-0014: shown before every
// restore) including collision detection.
type Preview struct {
	RecoveryPointID     string             `json:"recovery_point_id"`
	ApplicationName     string             `json:"application_name"`
	SourceHostID        string             `json:"source_host_id"`
	TargetHostID        string             `json:"target_host_id"`
	TargetHostname      string             `json:"target_hostname"`
	Mode                string             `json:"mode" enum:"in_place,alternate_host"`
	TargetApplicationID string             `json:"target_application_id,omitempty"`
	Production          bool               `json:"production"`
	ProductionReasons   []string           `json:"production_reasons,omitempty"`
	Components          []PreviewComponent `json:"components"`
	StopContainers      []PreviewContainer `json:"stop_containers"`
	CreateContainers    []string           `json:"create_containers"`
	Networks            []PreviewItem      `json:"networks"`
	Images              []PreviewImage     `json:"images"`
	Ports               []string           `json:"ports"`
	Collisions          []Collision        `json:"collisions"`
	Warnings            []string           `json:"warnings"`
	Blocked             bool               `json:"blocked"`
	stopIDs             []string           // plan data (not serialized)
	networkSpecs        []*agentv1.NetworkSpec
}

// PreviewComponent is one component's effect.
type PreviewComponent struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Action    string `json:"action" enum:"overwrite,create,restore_files,load_dump"`
	Target    string `json:"target"`
	SizeBytes int64  `json:"size_bytes"`
}

// PreviewContainer is a container that will be stopped.
type PreviewContainer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// PreviewItem is a network.
type PreviewItem struct {
	Name   string `json:"name"`
	Action string `json:"action" enum:"exists,create,missing_external"`
}

// PreviewImage is an image (present | pull).
type PreviewImage struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
	Action string `json:"action" enum:"present,pull" doc:"pull: not in the target host's latest inventory; the agent pulls it by digest only if it is really missing."`
}

// Collision blocks a restore: the target host has something that belongs
// to someone else.
type Collision struct {
	Kind   string `json:"kind" enum:"container_name,port,network,volume,bind_path,dependency"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

type previewInput struct {
	m              *manifest.Manifest
	targetAgentID  string
	targetHostname string
	inv            *inventory.Inventory // target host (nil = unknown)
	targetAppID    string               // "" = no such application on the target
	targetAppEnv   string
	manual         []string // target app's manual containers
	selected       []string
	remaps         []PathRemap
}

// Selectable reports whether a manifest component can be restored and
// normalizes a selection: fsmeta follows its parent, config is always
// available for container re-creation.
func selectComponents(m *manifest.Manifest, want []string) ([]manifest.Component, error) {
	byName := map[string]manifest.Component{}
	for _, c := range m.Components {
		byName[c.Name] = c
	}
	if len(want) == 0 {
		for _, c := range m.Components {
			if c.Kind != manifest.KindFSMeta && c.Kind != manifest.KindImage && c.Status == manifest.ComponentSucceeded {
				want = append(want, c.Name)
			}
		}
	}
	var out []manifest.Component
	seen := map[string]bool{}
	for _, n := range want {
		c, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("%w: the recovery point has no component %q", ErrInvalid, n)
		}
		if c.Status != manifest.ComponentSucceeded {
			return nil, fmt.Errorf("%w: component %s was not captured (%s)", ErrInvalid, n, c.Status)
		}
		if c.Kind == manifest.KindFSMeta || c.Kind == manifest.KindImage {
			return nil, fmt.Errorf("%w: component %s cannot be restored on its own", ErrInvalid, n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, c)
		}
	}
	return out, nil
}

func fsmetaFor(m *manifest.Manifest, parent string) *manifest.Component {
	for i := range m.Components {
		c := &m.Components[i]
		if c.Kind == manifest.KindFSMeta && c.Parent == parent && c.Status == manifest.ComponentSucceeded {
			return c
		}
	}
	return nil
}

// computePreview is pure: manifest + target inventory → impact and
// collisions.
func computePreview(in previewInput, selected []manifest.Component) *Preview {
	m := in.m
	p := &Preview{RecoveryPointID: m.RecoveryPointID, ApplicationName: m.Application.Name, SourceHostID: m.Source.AgentID,
		TargetHostID: in.targetAgentID, TargetHostname: in.targetHostname, TargetApplicationID: in.targetAppID,
		Components: []PreviewComponent{}, StopContainers: []PreviewContainer{}, CreateContainers: []string{},
		Networks: []PreviewItem{}, Images: []PreviewImage{}, Ports: []string{}, Collisions: []Collision{}, Warnings: []string{}}
	p.Mode = "alternate_host"
	if in.targetAgentID == m.Source.AgentID {
		p.Mode = "in_place"
	}
	project := m.Application.ComposeProject
	topo := m.Topology
	if topo == nil {
		p.Warnings = append(p.Warnings, "this recovery point predates topology capture: collision detection is limited to volumes and bind paths, and container re-creation relies on the config component only")
		topo = &manifest.Topology{}
	}
	ourNames := map[string]bool{}
	for _, c := range topo.Containers {
		ourNames[c.Name] = true
	}
	manual := map[string]bool{}
	for _, n := range in.manual {
		manual[n] = true
	}
	inv := in.inv
	if inv == nil {
		inv = &inventory.Inventory{}
		p.Warnings = append(p.Warnings, "no inventory has been collected from the target host yet: collisions cannot be checked")
		p.Blocked = true
	}
	isAgent := func(c inventory.Container) bool { return c.Labels[AgentRoleLabel] == "agent" }
	belongs := func(c inventory.Container) bool {
		if project != "" {
			return c.Labels[inventory.LabelProject] == project
		}
		name := strings.TrimPrefix(c.Name, "/")
		return manual[name] || ourNames[name]
	}
	// Containers are stopped only when files are swapped; database dumps
	// load into the running database.
	swapsFiles := false
	for _, c := range selected {
		if c.Kind != manifest.KindDatabase {
			swapsFiles = true
		}
	}
	existing := map[string]inventory.Container{}
	running := false
	for _, c := range inv.Containers {
		name := strings.TrimPrefix(c.Name, "/")
		existing[name] = c
		if belongs(c) {
			live := c.State == "running" || c.State == "paused" || c.State == "restarting"
			running = running || live
			if swapsFiles {
				p.StopContainers = append(p.StopContainers, PreviewContainer{ID: c.ID, Name: name, State: c.State})
				if live {
					p.stopIDs = append(p.stopIDs, c.ID)
				}
			}
		}
	}
	if running || len(p.StopContainers) > 0 || in.targetAppID != "" {
		p.Mode = "in_place"
	}

	// Production (final_stack → Production restores).
	if in.targetAppEnv == "production" {
		p.Production = true
		p.ProductionReasons = append(p.ProductionReasons, "the target application is tagged environment: production")
	}
	if running {
		p.Production = true
		p.ProductionReasons = append(p.ProductionReasons, "the restore overwrites a running application in place")
	}

	// Containers: names owned by others collide; missing ones are created.
	for _, c := range topo.Containers {
		if e, ok := existing[c.Name]; ok {
			if !belongs(e) {
				p.Collisions = append(p.Collisions, Collision{Kind: "container_name", Name: c.Name,
					Detail: "a container with this name exists on the target and belongs to " + owner(e)})
			}
			continue
		}
		p.CreateContainers = append(p.CreateContainers, c.Name)
	}
	// Ports published by containers that are not ours.
	used := map[string]string{}
	for _, c := range inv.Containers {
		if belongs(c) {
			continue
		}
		for _, pt := range c.Ports {
			if pt.HostPort != "" {
				used[pt.HostPort+"/"+pt.Protocol] = strings.TrimPrefix(c.Name, "/")
			}
		}
	}
	portSeen := map[string]bool{}
	for _, c := range topo.Containers {
		for _, pt := range c.Ports {
			if pt.HostPort == "" {
				continue
			}
			key := pt.HostPort + "/" + pt.Protocol
			if !portSeen[key] {
				portSeen[key] = true
				p.Ports = append(p.Ports, key)
			}
			if other, ok := used[key]; ok {
				p.Collisions = append(p.Collisions, Collision{Kind: "port", Name: key, Detail: "already published by container " + other})
			}
		}
	}
	sort.Strings(p.Ports)
	// Networks.
	nets := map[string]inventory.Network{}
	for _, n := range inv.Networks {
		nets[n.Name] = n
	}
	for _, n := range topo.Networks {
		switch n.Name {
		case "bridge", "host", "none":
			continue
		}
		e, ok := nets[n.Name]
		switch {
		case ok && !n.External && project != "" && e.Labels[inventory.LabelProject] != "" && e.Labels[inventory.LabelProject] != project:
			p.Collisions = append(p.Collisions, Collision{Kind: "network", Name: n.Name, Detail: "exists and belongs to Compose project " + e.Labels[inventory.LabelProject]})
		case ok:
			p.Networks = append(p.Networks, PreviewItem{Name: n.Name, Action: "exists"})
		case n.External:
			p.Networks = append(p.Networks, PreviewItem{Name: n.Name, Action: "missing_external"})
			p.Collisions = append(p.Collisions, Collision{Kind: "dependency", Name: n.Name, Detail: "external network is missing on the target; create it first"})
		default:
			p.Networks = append(p.Networks, PreviewItem{Name: n.Name, Action: "create"})
		}
		p.networkSpecs = append(p.networkSpecs, &agentv1.NetworkSpec{Name: n.Name, Driver: n.Driver, Labels: n.Labels,
			Internal: n.Internal, Attachable: n.Attachable, External: n.External})
	}
	// Images.
	have := map[string]bool{}
	for _, im := range inv.Images {
		for _, d := range im.RepoDigests {
			if i := strings.Index(d, "@"); i >= 0 {
				have[d[i+1:]] = true
			}
		}
		for _, t := range im.RepoTags {
			have["tag:"+t] = true
		}
	}
	for _, im := range m.Images {
		act := "pull"
		if (im.Digest != "" && have[digestOf(im.Digest)]) || (im.Digest == "" && have["tag:"+im.Ref]) {
			act = "present"
		}
		p.Images = append(p.Images, PreviewImage{Ref: im.Ref, Digest: im.Digest, Action: act})
	}
	// Components.
	vols := map[string]inventory.Volume{}
	for _, v := range inv.Volumes {
		vols[v.Name] = v
	}
	bindUsers := map[string]string{}
	for _, c := range inv.Containers {
		if belongs(c) || isAgent(c) {
			continue
		}
		for _, mt := range c.Mounts {
			if mt.Type == "bind" {
				bindUsers[mt.Source] = strings.TrimPrefix(c.Name, "/")
			}
		}
	}
	for _, c := range selected {
		pc := PreviewComponent{Name: c.Name, Kind: c.Kind, SizeBytes: c.SizeBytes}
		switch c.Kind {
		case manifest.KindVolume:
			pc.Target = c.VolumeName
			v, ok := vols[c.VolumeName]
			switch {
			case !ok:
				pc.Action = "create"
			case project != "" && v.Labels[inventory.LabelProject] != "" && v.Labels[inventory.LabelProject] != project:
				pc.Action = "overwrite"
				p.Collisions = append(p.Collisions, Collision{Kind: "volume", Name: c.VolumeName, Detail: "exists and belongs to Compose project " + v.Labels[inventory.LabelProject]})
			default:
				pc.Action = "overwrite"
			}
		case manifest.KindBindMount:
			pc.Target = Remap(c.Path, in.remaps)
			pc.Action = "overwrite"
			for src, who := range bindUsers {
				if src == pc.Target || strings.HasPrefix(src, pc.Target+"/") || strings.HasPrefix(pc.Target, src+"/") {
					p.Collisions = append(p.Collisions, Collision{Kind: "bind_path", Name: pc.Target, Detail: "path is mounted by container " + who + " (another application)"})
				}
			}
		case manifest.KindConfig:
			pc.Action, pc.Target = "restore_files", Remap(m.Application.WorkingDir, in.remaps)
		case manifest.KindDatabase:
			pc.Action = "load_dump"
			if c.Database != nil {
				pc.Target = c.Database.Engine + " in " + firstNonEmpty(c.Database.Container, c.Database.Service)
			}
		}
		p.Components = append(p.Components, pc)
	}
	if len(p.CreateContainers) > 0 && !hasConfig(m) {
		p.Collisions = append(p.Collisions, Collision{Kind: "dependency", Name: "config", Detail: "containers must be re-created but the recovery point has no config component"})
	}
	if len(p.Collisions) > 0 {
		p.Blocked = true
	}
	return p
}

func hasConfig(m *manifest.Manifest) bool {
	for _, c := range m.Components {
		if c.Kind == manifest.KindConfig && c.Status == manifest.ComponentSucceeded {
			return true
		}
	}
	return false
}

func digestOf(d string) string {
	if i := strings.Index(d, "@"); i >= 0 {
		return d[i+1:]
	}
	return d
}

func owner(c inventory.Container) string {
	if p := c.Labels[inventory.LabelProject]; p != "" {
		return "Compose project " + p
	}
	return "another application"
}
