// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Application kinds.
const (
	KindCompose   = "compose"
	KindContainer = "container"
	KindManual    = "manual"
)

// Compose definition provenance (roadmap Phase 3).
const (
	SourceOriginal      = "original"
	SourceReconstructed = "reconstructed"
)

// Volume classes (ADR-0006).
const (
	ClassLocal     = "local"
	ClassExternal  = "external"
	ClassEphemeral = "ephemeral"
)

// Application is the analyzed view of one application on one host.
type Application struct {
	Key            string          `json:"key"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	ComposeProject string          `json:"compose_project,omitempty"`
	WorkingDir     string          `json:"working_dir,omitempty"`
	Source         string          `json:"source"`
	SourceReason   string          `json:"source_reason,omitempty"`
	Services       []Service       `json:"services"`
	Volumes        []VolumeUse     `json:"volumes"`
	BindMounts     []BindUse       `json:"bind_mounts"`
	Tmpfs          []TmpfsUse      `json:"tmpfs"`
	Networks       []NetworkUse    `json:"networks"`
	Images         []ImageRef      `json:"images"`
	Dependencies   []Dependency    `json:"dependencies"`
	Unprotected    []Unprotected   `json:"unprotected"`
	SecretsCount   int             `json:"secrets_count"`
	Containers     []string        `json:"containers"`
	ConfigFiles    []File          `json:"-"`
	EnvFiles       []File          `json:"-"`
}

// Service is a Compose service (or a standalone container).
type Service struct {
	Name       string         `json:"name"`
	Image      string         `json:"image"`
	Containers []ContainerRef `json:"containers"`
}

// ContainerRef identifies a container.
type ContainerRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// VolumeUse is a named volume used by the application.
type VolumeUse struct {
	Name       string   `json:"name"`
	Driver     string   `json:"driver"`
	Mountpoint string   `json:"mountpoint"`
	Class      string   `json:"class"`
	Reasons    []string `json:"reasons,omitempty"`
	Anonymous  bool     `json:"anonymous,omitempty"`
	// External is true when the volume is not owned by the Compose project
	// (`external: true`).
	External bool     `json:"external,omitempty"`
	UsedBy   []string `json:"used_by"` // container:destination
	// Protected reports whether DBR² protects it by default (Local only).
	Protected bool `json:"protected_by_default"`
}

// BindUse is a bind mount.
type BindUse struct {
	Container   string `json:"container"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

// TmpfsUse is a tmpfs mount (never backed up).
type TmpfsUse struct {
	Container   string `json:"container"`
	Destination string `json:"destination"`
}

// NetworkUse is a network the application uses.
type NetworkUse struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	External bool   `json:"external,omitempty"`
}

// ImageRef is an image with digest and platform.
type ImageRef struct {
	Reference string   `json:"reference"`
	ImageID   string   `json:"image_id"`
	Digests   []string `json:"digests,omitempty"`
	Platform  string   `json:"platform,omitempty"`
}

// Dependency kinds.
const (
	DepExternalNetwork = "external_network"
	DepExternalVolume  = "external_volume"
	DepNetworkStorage  = "network_storage"
	DepSharedContainer = "container_network"
)

// Dependency is something outside the application that recovery needs.
type Dependency struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// Unprotected is a writable path in a container's filesystem that no volume
// or bind mount backs: it is lost when the container is recreated.
type Unprotected struct {
	Container string `json:"container"`
	Path      string `json:"path"`
	Files     int    `json:"files"`
	Severity  string `json:"severity"` // high | low
	Reason    string `json:"reason"`
}

// Analyze groups the inventory into applications. manual maps a manual
// application key to the container names it groups; those containers are
// removed from the standalone listing.
func Analyze(inv *Inventory, manual map[string][]string) []Application {
	vols := map[string]Volume{}
	for _, v := range inv.Volumes {
		vols[v.Name] = v
	}
	nets := map[string]Network{}
	for _, n := range inv.Networks {
		nets[n.Name] = n
	}
	imgs := map[string]Image{}
	for _, im := range inv.Images {
		imgs[im.ID] = im
	}
	projects := map[string]ComposeProject{}
	for _, p := range inv.ComposeProjects {
		projects[p.Name] = p
	}
	claimed := map[string]string{}
	for key, names := range manual {
		for _, n := range names {
			claimed[n] = key
		}
	}

	groups := map[string][]Container{}
	kinds := map[string]string{}
	for _, c := range inv.Containers {
		if c.Labels[LabelOneOff] == "True" {
			continue // `docker compose run` one-off containers
		}
		if key, ok := claimed[c.Name]; ok {
			groups[key] = append(groups[key], c)
			kinds[key] = KindManual
			continue
		}
		if p := c.Labels[LabelProject]; p != "" {
			key := KindCompose + ":" + p
			groups[key] = append(groups[key], c)
			kinds[key] = KindCompose
			continue
		}
		key := KindContainer + ":" + c.Name
		groups[key] = append(groups[key], c)
		kinds[key] = KindContainer
	}
	for key := range manual { // manual apps whose containers are all gone
		if _, ok := groups[key]; !ok {
			groups[key] = nil
			kinds[key] = KindManual
		}
	}

	var out []Application
	for key, cs := range groups {
		sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
		a := Application{Key: key, Kind: kinds[key], Name: strings.SplitN(key, ":", 2)[1]}
		if a.Kind == KindCompose {
			a.ComposeProject = a.Name
			if p, ok := projects[a.Name]; ok {
				a.WorkingDir, a.ConfigFiles, a.EnvFiles = p.WorkingDir, p.ConfigFiles, p.EnvFiles
				a.Source, a.SourceReason = composeProvenance(p)
			} else {
				a.Source, a.SourceReason = SourceReconstructed, "Compose files were not collected"
			}
		} else {
			a.Source, a.SourceReason = SourceReconstructed, "no Compose definition: generated from Docker runtime metadata"
		}
		analyzeContainers(&a, cs, vols, nets, imgs)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func composeProvenance(p ComposeProject) (string, string) {
	if len(p.ConfigFiles) == 0 {
		return SourceReconstructed, "no config_files label"
	}
	for _, f := range p.ConfigFiles {
		if f.Error != "" {
			return SourceReconstructed, "original Compose file unreadable: " + f.Path + ": " + f.Error
		}
		if f.Truncated {
			return SourceReconstructed, "original Compose file too large: " + f.Path
		}
	}
	return SourceOriginal, ""
}

func analyzeContainers(a *Application, cs []Container, vols map[string]Volume, nets map[string]Network, imgs map[string]Image) {
	services := map[string]*Service{}
	volUse := map[string]*VolumeUse{}
	netUse := map[string]*NetworkUse{}
	imgSeen := map[string]bool{}
	deps := map[string]Dependency{}
	for _, c := range cs {
		a.Containers = append(a.Containers, c.Name)
		svcName := c.Labels[LabelService]
		if svcName == "" {
			svcName = c.Name
		}
		s := services[svcName]
		if s == nil {
			s = &Service{Name: svcName, Image: c.Image}
			services[svcName] = s
		}
		s.Containers = append(s.Containers, ContainerRef{ID: c.ID, Name: c.Name, State: c.State})

		for _, e := range c.Env {
			if e.Sensitive {
				a.SecretsCount++
			}
		}
		for _, m := range c.Mounts {
			switch m.Type {
			case MountVolume:
				v := volUse[m.Name]
				if v == nil {
					vol := vols[m.Name]
					if vol.Name == "" {
						vol = Volume{Name: m.Name, Driver: m.Driver, Mountpoint: m.Source}
					}
					class, reasons := ClassifyVolume(vol, m.Destination)
					v = &VolumeUse{Name: vol.Name, Driver: vol.Driver, Mountpoint: vol.Mountpoint, Class: class, Reasons: reasons,
						Anonymous: isAnonymous(vol.Name), Protected: class == ClassLocal}
					if a.Kind == KindCompose && vol.Labels[LabelProject] != a.ComposeProject && !v.Anonymous {
						v.External = true
						deps["v:"+vol.Name] = Dependency{Kind: DepExternalVolume, Name: vol.Name,
							Detail: "volume is not created by the Compose project (external: true); it must exist before restore"}
					}
					if class == ClassExternal {
						deps["s:"+vol.Name] = Dependency{Kind: DepNetworkStorage, Name: vol.Name, Detail: strings.Join(reasons, "; ")}
					}
					volUse[m.Name] = v
				}
				v.UsedBy = append(v.UsedBy, c.Name+":"+m.Destination)
			case MountBind:
				a.BindMounts = append(a.BindMounts, BindUse{Container: c.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
			case MountTmpfs:
				a.Tmpfs = append(a.Tmpfs, TmpfsUse{Container: c.Name, Destination: m.Destination})
			}
		}
		for dest := range c.Tmpfs {
			a.Tmpfs = append(a.Tmpfs, TmpfsUse{Container: c.Name, Destination: dest})
		}
		for _, n := range c.Networks {
			if netUse[n] != nil {
				continue
			}
			net := nets[n]
			nu := &NetworkUse{Name: n, Driver: net.Driver}
			if a.Kind == KindCompose && !isBuiltinNetwork(n) && net.Labels[LabelProject] != a.ComposeProject {
				nu.External = true
				deps["n:"+n] = Dependency{Kind: DepExternalNetwork, Name: n,
					Detail: "network is not created by the Compose project (external: true); it must exist before restore"}
			}
			netUse[n] = nu
		}
		if strings.HasPrefix(c.NetworkMode, "container:") {
			deps["c:"+c.NetworkMode] = Dependency{Kind: DepSharedContainer, Name: strings.TrimPrefix(c.NetworkMode, "container:"),
				Detail: c.Name + " shares another container's network namespace"}
		}
		if !imgSeen[c.Image+"|"+c.ImageID] {
			imgSeen[c.Image+"|"+c.ImageID] = true
			ref := ImageRef{Reference: c.Image, ImageID: c.ImageID}
			if im, ok := imgs[c.ImageID]; ok {
				ref.Digests = im.RepoDigests
				ref.Platform = im.OS + "/" + im.Architecture
				if im.Variant != "" {
					ref.Platform += "/" + im.Variant
				}
			}
			a.Images = append(a.Images, ref)
		}
		a.Unprotected = append(a.Unprotected, UnprotectedPaths(c)...)
	}
	for _, s := range services {
		a.Services = append(a.Services, *s)
	}
	sort.Slice(a.Services, func(i, j int) bool { return a.Services[i].Name < a.Services[j].Name })
	for _, v := range volUse {
		a.Volumes = append(a.Volumes, *v)
	}
	sort.Slice(a.Volumes, func(i, j int) bool { return a.Volumes[i].Name < a.Volumes[j].Name })
	for _, n := range netUse {
		a.Networks = append(a.Networks, *n)
	}
	sort.Slice(a.Networks, func(i, j int) bool { return a.Networks[i].Name < a.Networks[j].Name })
	for _, d := range deps {
		a.Dependencies = append(a.Dependencies, d)
	}
	sort.Slice(a.Dependencies, func(i, j int) bool {
		return a.Dependencies[i].Kind+a.Dependencies[i].Name < a.Dependencies[j].Kind+a.Dependencies[j].Name
	})
	for _, f := range a.ConfigFiles {
		if f.Masked {
			a.SecretsCount++
		}
	}
	for _, f := range a.EnvFiles {
		if f.Masked {
			a.SecretsCount++
		}
	}
	nonNil(a)
}

func nonNil(a *Application) {
	if a.Services == nil {
		a.Services = []Service{}
	}
	if a.Volumes == nil {
		a.Volumes = []VolumeUse{}
	}
	if a.BindMounts == nil {
		a.BindMounts = []BindUse{}
	}
	if a.Tmpfs == nil {
		a.Tmpfs = []TmpfsUse{}
	}
	if a.Networks == nil {
		a.Networks = []NetworkUse{}
	}
	if a.Images == nil {
		a.Images = []ImageRef{}
	}
	if a.Dependencies == nil {
		a.Dependencies = []Dependency{}
	}
	if a.Unprotected == nil {
		a.Unprotected = []Unprotected{}
	}
	if a.Containers == nil {
		a.Containers = []string{}
	}
}

var anonymousVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

func isAnonymous(name string) bool { return anonymousVolume.MatchString(name) }

func isBuiltinNetwork(n string) bool { return n == "bridge" || n == "host" || n == "none" }

var networkFSTypes = map[string]bool{"nfs": true, "nfs4": true, "cifs": true, "smb": true, "smb3": true,
	"ceph": true, "glusterfs": true, "fuse.sshfs": true, "sshfs": true}

var ephemeralPaths = []string{"/tmp", "/var/tmp", "/var/cache", "/cache", "/root/.cache", "/run", "/var/run"}
var ephemeralName = regexp.MustCompile(`(^|[_.-])(cache|caches|tmp|temp)([_.-]|$)`)

// ClassifyVolume classifies a volume (ADR-0006): External volumes (non-local
// driver, or local driver with network filesystem options) belong to an
// external storage system and are not backed up by default; Ephemeral volumes
// hold caches/temp data; everything else is Local.
func ClassifyVolume(v Volume, destination string) (string, []string) {
	if v.Driver != "" && v.Driver != "local" {
		return ClassExternal, []string{"volume driver " + v.Driver + " (data owned by an external storage system)"}
	}
	if t := strings.ToLower(v.Options["type"]); networkFSTypes[t] {
		r := "local driver mounts a " + t + " filesystem"
		if dev := v.Options["device"]; dev != "" {
			r += " (" + dev + ")"
		}
		if o := v.Options["o"]; strings.Contains(o, "addr=") {
			r += " [" + o + "]"
		}
		return ClassExternal, []string{r}
	}
	if strings.ToLower(v.Options["type"]) == "tmpfs" {
		return ClassEphemeral, []string{"tmpfs-backed volume"}
	}
	for _, p := range ephemeralPaths {
		if destination == p || strings.HasPrefix(destination, p+"/") {
			return ClassEphemeral, []string{"mounted at " + destination}
		}
	}
	if ephemeralName.MatchString(strings.ToLower(v.Name)) {
		return ClassEphemeral, []string{"name suggests cache or temporary data"}
	}
	return ClassLocal, nil
}

// Paths never considered application data: kernel/virtual filesystems, temp
// dirs, and the files Docker itself bind-mounts into every container.
var systemPrefixes = []string{"/proc", "/sys", "/dev", "/tmp", "/var/tmp", "/run", "/var/run"}
var dockerManaged = map[string]bool{"/etc/hosts": true, "/etc/hostname": true, "/etc/resolv.conf": true, "/.dockerenv": true}
var lowSeverityPrefixes = []string{"/var/log", "/var/cache", "/root/.cache", "/root/.npm", "/var/lib/apt", "/var/lib/dpkg", "/var/lib/rpm", "/etc",
	"/usr/local/share/ca-certificates", "/usr/share/ca-certificates"}

// UnprotectedPaths finds writable-layer paths not backed by any mount — data
// that disappears when the container is recreated (core feature).
func UnprotectedPaths(c Container) []Unprotected {
	if c.ReadOnlyRootfs || len(c.Changes) == 0 {
		return nil
	}
	var mountDests []string
	for _, m := range c.Mounts {
		mountDests = append(mountDests, m.Destination)
	}
	for d := range c.Tmpfs {
		mountDests = append(mountDests, d)
	}
	var paths []string
	for _, ch := range c.Changes {
		if ch.Kind == "D" {
			continue
		}
		p := path.Clean(ch.Path)
		if dockerManaged[p] || under(p, systemPrefixes) || under(p, mountDests) || ancestorOf(p, mountDests) {
			continue // ancestors of mount points are directories Docker created for the mount
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// Drop parent directories reported as "changed" because a child changed.
	var leaves []string
	for i, p := range paths {
		if i+1 < len(paths) && strings.HasPrefix(paths[i+1], p+"/") {
			continue
		}
		leaves = append(leaves, p)
	}
	// Severity is judged per file; a directory group is high if any file is.
	type agg struct{ files, high int }
	groups := map[string]*agg{}
	for _, p := range leaves {
		g := groups[groupPath(p)]
		if g == nil {
			g = &agg{}
			groups[groupPath(p)] = g
		}
		g.files++
		if !(under(p, lowSeverityPrefixes) || strings.Contains(p, "/cache") || strings.Contains(p, "/.cache")) {
			g.high++
		}
	}
	var out []Unprotected
	for path, g := range groups {
		sev, reason := "high", "writable path not backed by a volume or bind mount: lost when the container is recreated"
		if g.high == 0 {
			sev, reason = "low", "container-local logs, caches, certificates or runtime config: usually regenerated, but lost on recreation"
		}
		out = append(out, Unprotected{Container: c.Name, Path: path, Files: g.files, Severity: sev, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == "high"
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// groupPath aggregates files to their data directory: /app/reports/x.pdf →
// /app/reports; /var/lib/custom-data/f → /var/lib/custom-data.
func groupPath(p string) string {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	depth := 2
	switch segs[0] {
	case "var", "usr", "opt", "srv", "home":
		depth = 3
	}
	if len(segs) <= depth {
		if len(segs) > 1 {
			return "/" + strings.Join(segs[:len(segs)-1], "/")
		}
		return "/" + segs[0]
	}
	return "/" + strings.Join(segs[:depth], "/")
}

// ancestorOf reports whether p is a parent directory of any of paths.
func ancestorOf(p string, paths []string) bool {
	for _, x := range paths {
		if strings.HasPrefix(x, strings.TrimRight(p, "/")+"/") {
			return true
		}
	}
	return false
}

func under(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if pre == "" {
			continue
		}
		if p == pre || strings.HasPrefix(p, strings.TrimRight(pre, "/")+"/") {
			return true
		}
	}
	return false
}
