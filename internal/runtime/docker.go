// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/AxiomOperator/dbr2/internal/inventory"
)

// Collection limits keep a single discovery bounded.
const (
	maxChangesPerContainer = 2000
	maxFileBytes           = 1 << 20 // 1 MiB per Compose/.env file
)

// DockerRuntime discovers a rootful Docker Engine through the Moby Go SDK
// (ADR-0006: never shells out to the docker CLI; paths come from Docker).
type DockerRuntime struct {
	cli *client.Client
	// ReadFile reads host files (Compose definitions); replaceable in tests.
	ReadFile func(path string) (data []byte, size int64, truncated bool, err error)
}

// NewDocker connects to the engine at host ("" = DOCKER_HOST or the default socket).
func NewDocker(host string) (*DockerRuntime, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, err
	}
	return &DockerRuntime{cli: cli, ReadFile: readHostFile}, nil
}

// Name implements ContainerRuntime.
func (d *DockerRuntime) Name() string { return "docker" }

// Close implements ContainerRuntime.
func (d *DockerRuntime) Close() error { return d.cli.Close() }

// Ping implements ContainerRuntime.
func (d *DockerRuntime) Ping(ctx context.Context) (string, error) {
	v, err := d.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// Discover implements ContainerRuntime.
func (d *DockerRuntime) Discover(ctx context.Context, opts DiscoverOptions) (*inventory.Inventory, error) {
	inv := &inventory.Inventory{SchemaVersion: inventory.SchemaVersion, CollectedAt: time.Now().UTC()}

	info, err := d.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker info: %w", err)
	}
	ver, err := d.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker version: %w", err)
	}
	i := info.Info
	inv.Host = inventory.Host{
		Hostname: i.Name, OperatingSystem: i.OperatingSystem, OSType: i.OSType, KernelVersion: i.KernelVersion,
		Architecture: i.Architecture, Runtime: "docker", EngineVersion: ver.Version, APIVersion: ver.APIVersion,
		RootDir: i.DockerRootDir, StorageDriver: i.Driver, CgroupVersion: i.CgroupVersion,
		SecurityOptions: i.SecurityOptions, CPUs: i.NCPU, MemoryBytes: i.MemTotal,
	}
	for _, o := range i.SecurityOptions {
		if strings.Contains(o, "name=rootless") {
			inv.Host.Rootless = true
		}
		if strings.Contains(o, "name=selinux") {
			inv.Host.SELinux = true
		}
	}

	list, err := d.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	imageIDs := map[string]bool{}
	for _, s := range list.Items {
		c, err := d.container(ctx, s.ID, opts)
		if err != nil {
			inv.Warnings = append(inv.Warnings, fmt.Sprintf("container %s: %v", s.ID[:12], err))
			continue
		}
		imageIDs[c.ImageID] = true
		inv.Containers = append(inv.Containers, c)
	}
	sort.Slice(inv.Containers, func(a, b int) bool { return inv.Containers[a].Name < inv.Containers[b].Name })

	vols, err := d.cli.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	for _, v := range vols.Items {
		inv.Volumes = append(inv.Volumes, inventory.Volume{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Scope: v.Scope,
			Labels: v.Labels, Options: v.Options, CreatedAt: v.CreatedAt,
		})
	}
	inv.Warnings = append(inv.Warnings, vols.Warnings...)

	nets, err := d.cli.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	for _, n := range nets.Items {
		inv.Networks = append(inv.Networks, inventory.Network{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal,
			Attachable: n.Attachable, Labels: n.Labels,
		})
	}

	for id := range imageIDs {
		im, err := d.cli.ImageInspect(ctx, id)
		if err != nil {
			inv.Warnings = append(inv.Warnings, fmt.Sprintf("image %s: %v", short(id), err))
			continue
		}
		inv.Images = append(inv.Images, inventory.Image{
			ID: im.ID, RepoTags: im.RepoTags, RepoDigests: im.RepoDigests,
			OS: im.Os, Architecture: im.Architecture, Variant: im.Variant, Size: im.Size,
		})
	}
	sort.Slice(inv.Images, func(a, b int) bool { return inv.Images[a].ID < inv.Images[b].ID })

	inv.ComposeProjects = d.composeProjects(inv.Containers)
	return inv, nil
}

func (d *DockerRuntime) container(ctx context.Context, id string, opts DiscoverOptions) (inventory.Container, error) {
	res, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return inventory.Container{}, err
	}
	r := res.Container
	c := inventory.Container{ID: r.ID, Name: strings.TrimPrefix(r.Name, "/"), ImageID: r.Image}
	if t, err := time.Parse(time.RFC3339Nano, r.Created); err == nil {
		c.Created = t
	}
	if r.State != nil {
		c.State = string(r.State.Status)
	}
	if cfg := r.Config; cfg != nil {
		c.Image, c.Labels, c.Cmd, c.Entrypoint, c.User, c.WorkingDir = cfg.Image, cfg.Labels, cfg.Cmd, cfg.Entrypoint, cfg.User, cfg.WorkingDir
		for _, kv := range cfg.Env {
			k, v, _ := strings.Cut(kv, "=")
			c.Env = append(c.Env, inventory.EnvVar{Key: k, Value: v})
		}
	}
	if hc := r.HostConfig; hc != nil {
		c.RestartPolicy = string(hc.RestartPolicy.Name)
		c.ReadOnlyRootfs = hc.ReadonlyRootfs
		c.Tmpfs = hc.Tmpfs
		c.NetworkMode = string(hc.NetworkMode)
		for port, binds := range hc.PortBindings {
			for _, b := range binds {
				p := inventory.Port{ContainerPort: fmt.Sprint(port.Num()), Protocol: string(port.Proto()), HostPort: b.HostPort}
				if b.HostIP.IsValid() {
					p.HostIP = b.HostIP.String()
				}
				c.Ports = append(c.Ports, p)
			}
		}
		sort.Slice(c.Ports, func(a, b int) bool { return c.Ports[a].ContainerPort+c.Ports[a].HostPort < c.Ports[b].ContainerPort+c.Ports[b].HostPort })
	}
	for _, m := range r.Mounts {
		c.Mounts = append(c.Mounts, inventory.Mount{
			Type: string(m.Type), Name: m.Name, Source: m.Source, Destination: m.Destination,
			Driver: m.Driver, RW: m.RW, Propagation: string(m.Propagation), Mode: m.Mode,
		})
	}
	if ns := r.NetworkSettings; ns != nil {
		for name := range ns.Networks {
			c.Networks = append(c.Networks, name)
		}
		sort.Strings(c.Networks)
	}
	if opts.FilesystemChanges && !c.ReadOnlyRootfs {
		diff, err := d.cli.ContainerDiff(ctx, r.ID, client.ContainerDiffOptions{})
		if err != nil {
			c.ChangesError = err.Error()
		} else {
			for i, ch := range diff.Changes {
				if i >= maxChangesPerContainer {
					c.ChangesTruncated = true
					break
				}
				c.Changes = append(c.Changes, inventory.Change{Path: ch.Path, Kind: ch.Kind.String()})
			}
		}
	}
	return c, nil
}

// composeProjects reads the original Compose files of hand-deployed projects
// from the paths Compose recorded in its labels (plus the project .env).
func (d *DockerRuntime) composeProjects(cs []inventory.Container) []inventory.ComposeProject {
	byName := map[string]*inventory.ComposeProject{}
	var order []string
	for _, c := range cs {
		name := c.Labels[inventory.LabelProject]
		if name == "" || byName[name] != nil {
			continue
		}
		p := &inventory.ComposeProject{Name: name, WorkingDir: c.Labels[inventory.LabelWorkingDir]}
		for _, f := range splitList(c.Labels[inventory.LabelConfigFiles]) {
			p.ConfigFiles = append(p.ConfigFiles, d.file(resolve(p.WorkingDir, f)))
		}
		envFiles := splitList(c.Labels[inventory.LabelEnvFiles])
		if len(envFiles) == 0 && p.WorkingDir != "" {
			if _, err := os.Stat(filepath.Join(p.WorkingDir, ".env")); err == nil {
				envFiles = []string{".env"}
			}
		}
		for _, f := range envFiles {
			p.EnvFiles = append(p.EnvFiles, d.file(resolve(p.WorkingDir, f)))
		}
		byName[name] = p
		order = append(order, name)
	}
	sort.Strings(order)
	out := make([]inventory.ComposeProject, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out
}

func (d *DockerRuntime) file(path string) inventory.File {
	f := inventory.File{Path: path}
	data, size, truncated, err := d.ReadFile(path)
	f.Size, f.Truncated = size, truncated
	if err != nil {
		f.Error = err.Error()
		return f
	}
	if !truncated {
		f.Content = string(data)
	}
	return f
}

func readHostFile(path string) ([]byte, int64, bool, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, st.Size(), false, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(fh, maxFileBytes+1))
	if err != nil {
		return nil, st.Size(), false, err
	}
	if len(data) > maxFileBytes {
		return nil, st.Size(), true, nil
	}
	return data, st.Size(), false, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) || dir == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
