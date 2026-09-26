// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// ErrNotFound wraps engine "no such object" errors of the RestoreControl
// inspect methods.
var ErrNotFound = errors.New("not found")

// ContainerDetails is the container state restore commands need.
type ContainerDetails struct {
	ID    string
	Name  string
	State string
	// Running is true for running, paused and restarting containers.
	Running      bool
	ExitCode     int
	RestartCount int
	// Health is healthy | unhealthy | starting, or "none" without a
	// healthcheck.
	Health string
	// Env is the configured environment (contains secrets; never log it).
	Env []string
}

// VolumeInfo describes a volume.
type VolumeInfo struct {
	Name       string
	Driver     string
	Mountpoint string
	Labels     map[string]string
}

// NetworkSpec describes a network to create.
type NetworkSpec struct {
	Name       string
	Driver     string
	Labels     map[string]string
	Internal   bool
	Attachable bool
}

// ImageInfo describes a local image.
type ImageInfo struct {
	ID          string
	RepoTags    []string
	RepoDigests []string
}

// ContainerSpec is a container create request derived from a captured
// inspect document (ContainerSpecFromInspect).
type ContainerSpec struct {
	Name  string
	Image string
	// WasRunning: the container was running (or paused) at capture.
	WasRunning bool
	// Networks the container attaches to (user-defined and built-in).
	Networks []string
	// Create is the engine create request (JSON: Config, HostConfig,
	// NetworkingConfig). It holds the real environment; never log it.
	Create json.RawMessage
}

// RestoreControl is implemented by runtimes that can restore applications
// (Phase 5, ADR-0006): images, volumes, networks, container creation and
// database loads. The agent type-asserts for it.
type RestoreControl interface {
	ContainerControl
	// InspectDetails returns the container's state, health and environment
	// (ErrNotFound for an unknown id or name).
	InspectDetails(ctx context.Context, id string) (ContainerDetails, error)
	// Logs returns the last tail lines of stdout+stderr.
	Logs(ctx context.Context, id string, tail int) (string, error)
	// ExecInput runs cmd with stdin streamed from in (closed at EOF). An
	// error reading in aborts the exec and is returned.
	ExecInput(ctx context.Context, id string, cmd []string, in io.Reader, maxOutput int) (ExecResult, error)
	// CopyToContainer extracts a tar stream into dir inside the container
	// (which may be stopped).
	CopyToContainer(ctx context.Context, id, dir string, tarStream io.Reader) error
	// CreateContainer creates (does not start) a container.
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	// RemoveContainer force-removes a container (not its volumes).
	RemoveContainer(ctx context.Context, id string) error
	InspectVolume(ctx context.Context, name string) (VolumeInfo, error)
	CreateVolume(ctx context.Context, name, driver string, labels map[string]string) (VolumeInfo, error)
	RemoveVolume(ctx context.Context, name string) error
	NetworkExists(ctx context.Context, name string) (bool, error)
	CreateNetwork(ctx context.Context, spec NetworkSpec) (string, error)
	RemoveNetwork(ctx context.Context, name string) error
	// InspectImage resolves a reference (name:tag, name@digest or ID).
	InspectImage(ctx context.Context, ref string) (ImageInfo, error)
	// PullImage pulls ref (anonymous registry access).
	PullImage(ctx context.Context, ref string) error
	TagImage(ctx context.Context, source, target string) error
}

var _ RestoreControl = (*DockerRuntime)(nil)

func notFound(err error) error {
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}

// InspectDetails implements RestoreControl.
func (d *DockerRuntime) InspectDetails(ctx context.Context, id string) (ContainerDetails, error) {
	res, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerDetails{}, notFound(err)
	}
	c := res.Container
	out := ContainerDetails{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), RestartCount: c.RestartCount, Health: "none"}
	if s := c.State; s != nil {
		out.State, out.Running, out.ExitCode = string(s.Status), s.Running, s.ExitCode
		if s.Health != nil && s.Health.Status != "" {
			out.Health = string(s.Health.Status)
		}
	}
	if c.Config != nil {
		out.Env = c.Config.Env
	}
	return out, nil
}

// Logs implements RestoreControl.
func (d *DockerRuntime) Logs(ctx context.Context, id string, tail int) (string, error) {
	res, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return "", notFound(err)
	}
	rc, err := d.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: fmt.Sprint(tail)})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	out := NewTailBuffer(64 << 10)
	if res.Container.Config != nil && res.Container.Config.Tty {
		_, err = io.Copy(out, rc)
	} else {
		_, err = stdcopy.StdCopy(out, out, rc)
	}
	return string(out.Bytes()), err
}

// ExecInput implements RestoreControl.
func (d *DockerRuntime) ExecInput(ctx context.Context, id string, cmd []string, in io.Reader, maxOutput int) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, errors.New("empty command")
	}
	cr, err := d.cli.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: cmd, AttachStdin: true, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return ExecResult{}, err
	}
	at, err := d.cli.ExecAttach(ctx, cr.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, err
	}
	defer at.Close()
	stop := context.AfterFunc(ctx, at.Close)
	defer stop()
	inErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(at.Conn, in)
		if err != nil {
			at.Close() // abort: the command must not see a truncated input as complete
		} else {
			err = at.CloseWrite()
		}
		inErr <- err
	}()
	out := NewTailBuffer(maxOutput)
	_, cerr := stdcopy.StdCopy(out, out, at.Reader)
	ierr := <-inErr
	res := ExecResult{Output: out.Bytes(), Truncated: out.Truncated()}
	switch {
	case ctx.Err() != nil:
		return res, ctx.Err()
	case ierr != nil:
		return res, fmt.Errorf("stream input: %w", ierr)
	case cerr != nil:
		return res, cerr
	}
	ins, err := d.cli.ExecInspect(ctx, cr.ID, client.ExecInspectOptions{})
	if err != nil {
		return res, err
	}
	res.ExitCode = ins.ExitCode
	return res, nil
}

// CopyToContainer implements RestoreControl.
func (d *DockerRuntime) CopyToContainer(ctx context.Context, id, dir string, tarStream io.Reader) error {
	_, err := d.cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: dir, Content: tarStream})
	return err
}

// createBody is ContainerSpec.Create.
type createBody struct {
	Config           *container.Config         `json:"Config"`
	HostConfig       *container.HostConfig     `json:"HostConfig"`
	NetworkingConfig *network.NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

// CreateContainer implements RestoreControl.
func (d *DockerRuntime) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	var b createBody
	if err := json.Unmarshal(spec.Create, &b); err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	res, err := d.cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: spec.Name, Config: b.Config,
		HostConfig: b.HostConfig, NetworkingConfig: b.NetworkingConfig})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// RemoveContainer implements RestoreControl.
func (d *DockerRuntime) RemoveContainer(ctx context.Context, id string) error {
	_, err := d.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	return notFound(err)
}

// InspectVolume implements RestoreControl.
func (d *DockerRuntime) InspectVolume(ctx context.Context, name string) (VolumeInfo, error) {
	res, err := d.cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return VolumeInfo{}, notFound(err)
	}
	v := res.Volume
	return VolumeInfo{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Labels: v.Labels}, nil
}

// CreateVolume implements RestoreControl.
func (d *DockerRuntime) CreateVolume(ctx context.Context, name, driver string, labels map[string]string) (VolumeInfo, error) {
	res, err := d.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Driver: driver, Labels: labels})
	if err != nil {
		return VolumeInfo{}, err
	}
	v := res.Volume
	return VolumeInfo{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Labels: v.Labels}, nil
}

// RemoveVolume implements RestoreControl.
func (d *DockerRuntime) RemoveVolume(ctx context.Context, name string) error {
	_, err := d.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	return notFound(err)
}

// NetworkExists implements RestoreControl.
func (d *DockerRuntime) NetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := d.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// CreateNetwork implements RestoreControl.
func (d *DockerRuntime) CreateNetwork(ctx context.Context, s NetworkSpec) (string, error) {
	res, err := d.cli.NetworkCreate(ctx, s.Name, client.NetworkCreateOptions{Driver: s.Driver, Labels: s.Labels,
		Internal: s.Internal, Attachable: s.Attachable})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// RemoveNetwork implements RestoreControl.
func (d *DockerRuntime) RemoveNetwork(ctx context.Context, name string) error {
	_, err := d.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
	return notFound(err)
}

// InspectImage implements RestoreControl.
func (d *DockerRuntime) InspectImage(ctx context.Context, ref string) (ImageInfo, error) {
	res, err := d.cli.ImageInspect(ctx, ref)
	if err != nil {
		return ImageInfo{}, notFound(err)
	}
	return ImageInfo{ID: res.ID, RepoTags: res.RepoTags, RepoDigests: res.RepoDigests}, nil
}

// PullImage implements RestoreControl.
func (d *DockerRuntime) PullImage(ctx context.Context, ref string) error {
	resp, err := d.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	return resp.Wait(ctx)
}

// TagImage implements RestoreControl.
func (d *DockerRuntime) TagImage(ctx context.Context, source, target string) error {
	_, err := d.cli.ImageTag(ctx, client.ImageTagOptions{Source: source, Target: target})
	return err
}

// ContainerSpecFromInspect turns a captured inspect document into a create
// request: host paths of bind mounts are passed through remap, and
// runtime-only data is dropped (container-specific hostname, operational
// endpoint data such as assigned IPs/MACs, the container ID file). Static
// IPs (IPAMConfig), aliases and all other settings are kept.
func ContainerSpecFromInspect(raw []byte, remap func(string) string) (ContainerSpec, error) {
	var ins container.InspectResponse
	if err := json.Unmarshal(raw, &ins); err != nil {
		return ContainerSpec{}, fmt.Errorf("inspect document: %w", err)
	}
	if ins.Config == nil || ins.Config.Image == "" {
		return ContainerSpec{}, errors.New("inspect document has no Config.Image")
	}
	if remap == nil {
		remap = func(p string) string { return p }
	}
	spec := ContainerSpec{Name: strings.TrimPrefix(ins.Name, "/"), Image: ins.Config.Image}
	if spec.Name == "" {
		return ContainerSpec{}, errors.New("inspect document has no Name")
	}
	if s := ins.State; s != nil {
		spec.WasRunning = s.Running || s.Paused || s.Restarting
	}
	cfg := *ins.Config
	if len(ins.ID) >= 12 && cfg.Hostname == ins.ID[:12] {
		cfg.Hostname = "" // the engine default (short ID) of the old container
	}
	hc := &container.HostConfig{}
	if ins.HostConfig != nil {
		h := *ins.HostConfig
		hc = &h
	}
	hc.ContainerIDFile = ""
	binds := make([]string, 0, len(hc.Binds))
	for _, b := range hc.Binds {
		src, rest, ok := strings.Cut(b, ":")
		if ok && strings.HasPrefix(src, "/") {
			b = remap(src) + ":" + rest
		}
		binds = append(binds, b)
	}
	hc.Binds = binds
	mounts := make([]mount.Mount, 0, len(hc.Mounts))
	for _, m := range hc.Mounts {
		if m.Type == mount.TypeBind && strings.HasPrefix(m.Source, "/") {
			m.Source = remap(m.Source)
		}
		mounts = append(mounts, m)
	}
	hc.Mounts = mounts
	links := make([]string, 0, len(hc.Links))
	for _, l := range hc.Links { // inspect: "/db:/web/db" → create: "db:db"
		target, alias, ok := strings.Cut(l, ":")
		if ok {
			l = strings.TrimPrefix(target, "/") + ":" + alias[strings.LastIndex(alias, "/")+1:]
		}
		links = append(links, l)
	}
	hc.Links = links

	var nc *network.NetworkingConfig
	if ns := ins.NetworkSettings; ns != nil {
		for name := range ns.Networks {
			spec.Networks = append(spec.Networks, name)
		}
		sort.Strings(spec.Networks)
		mode := hc.NetworkMode
		if !mode.IsHost() && !mode.IsNone() && !mode.IsContainer() && len(ns.Networks) > 0 {
			nc = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
			for name, ep := range ns.Networks {
				e := &network.EndpointSettings{}
				if ep != nil {
					e.Links, e.DriverOpts, e.GwPriority = ep.Links, ep.DriverOpts, ep.GwPriority
					if c := ep.IPAMConfig; c != nil && (c.IPv4Address.IsValid() || c.IPv6Address.IsValid() || len(c.LinkLocalIPs) > 0) {
						e.IPAMConfig = c.Copy()
					}
					for _, a := range ep.Aliases {
						if len(ins.ID) >= 12 && a == ins.ID[:12] {
							continue // engine-added short-ID alias
						}
						e.Aliases = append(e.Aliases, a)
					}
				}
				nc.EndpointsConfig[name] = e
			}
		}
	}
	b, err := json.Marshal(createBody{Config: &cfg, HostConfig: hc, NetworkingConfig: nc})
	if err != nil {
		return ContainerSpec{}, err
	}
	spec.Create = b
	return spec, nil
}
