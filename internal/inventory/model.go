// SPDX-License-Identifier: Apache-2.0

// Package inventory defines the discovery document the agent sends to the
// gateway (DiscoverResult / InventoryReport) and the server-side analysis
// that turns it into Applications (Phase 3). The agent reports raw facts;
// classification, grouping, dependency and unprotected-data analysis happen
// on the server so policy changes do not require agent upgrades.
package inventory

import "time"

// SchemaVersion is the current inventory document version.
const SchemaVersion = 1

// Inventory is one discovery of a host.
type Inventory struct {
	SchemaVersion   int              `json:"schema_version"`
	CollectedAt     time.Time        `json:"collected_at"`
	Host            Host             `json:"host"`
	Containers      []Container      `json:"containers"`
	Volumes         []Volume         `json:"volumes"`
	Networks        []Network        `json:"networks"`
	Images          []Image          `json:"images"`
	ComposeProjects []ComposeProject `json:"compose_projects"`
	Warnings        []string         `json:"warnings,omitempty"`
}

// Host describes the container engine and its host.
type Host struct {
	Hostname        string   `json:"hostname"`
	OperatingSystem string   `json:"operating_system"`
	OSType          string   `json:"os_type"`
	KernelVersion   string   `json:"kernel_version"`
	Architecture    string   `json:"architecture"`
	Runtime         string   `json:"runtime"` // docker
	EngineVersion   string   `json:"engine_version"`
	APIVersion      string   `json:"api_version"`
	RootDir         string   `json:"root_dir"` // Info().DockerRootDir — never hard-coded
	StorageDriver   string   `json:"storage_driver"`
	CgroupVersion   string   `json:"cgroup_version"`
	SecurityOptions []string `json:"security_options,omitempty"`
	Rootless        bool     `json:"rootless"`
	SELinux         bool     `json:"selinux"`
	CPUs            int      `json:"cpus"`
	MemoryBytes     int64    `json:"memory_bytes"`
}

// Container is one container (running or stopped).
type Container struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Image          string            `json:"image"` // reference as configured
	ImageID        string            `json:"image_id"`
	State          string            `json:"state"`
	Created        time.Time         `json:"created"`
	Labels         map[string]string `json:"labels,omitempty"`
	Env            []EnvVar          `json:"env,omitempty"`
	Mounts         []Mount           `json:"mounts,omitempty"`
	Networks       []string          `json:"networks,omitempty"`
	NetworkMode    string            `json:"network_mode,omitempty"`
	Ports          []Port            `json:"ports,omitempty"`
	RestartPolicy  string            `json:"restart_policy,omitempty"`
	Cmd            []string          `json:"cmd,omitempty"`
	Entrypoint     []string          `json:"entrypoint,omitempty"`
	User           string            `json:"user,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	ReadOnlyRootfs bool              `json:"read_only_rootfs,omitempty"`
	Tmpfs          map[string]string `json:"tmpfs,omitempty"`
	// Changes are the writable-layer changes (docker diff), used for
	// unprotected-data detection. Capped by the agent.
	Changes          []Change `json:"changes,omitempty"`
	ChangesTruncated bool     `json:"changes_truncated,omitempty"`
	ChangesError     string   `json:"changes_error,omitempty"`
}

// EnvVar is one environment variable. The server seals sensitive values
// (Value emptied, Sealed set) before storing the inventory.
type EnvVar struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	Sealed    []byte `json:"sealed,omitempty"`
}

// Mount types.
const (
	MountVolume = "volume"
	MountBind   = "bind"
	MountTmpfs  = "tmpfs"
)

// Mount is a container mount. Source is the host path resolved by Docker
// (for volumes: the volume mountpoint).
type Mount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	Driver      string `json:"driver,omitempty"`
	RW          bool   `json:"rw"`
	Propagation string `json:"propagation,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

// Port is a published port.
type Port struct {
	ContainerPort string `json:"container_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip,omitempty"`
	HostPort      string `json:"host_port,omitempty"`
}

// Change is one writable-layer change: Kind is A (added), C (changed), D (deleted).
type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// Volume is a named volume.
type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Scope      string            `json:"scope,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Options    map[string]string `json:"options,omitempty"`
	CreatedAt  string            `json:"created_at,omitempty"`
}

// Network is a Docker network.
type Network struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope,omitempty"`
	Internal   bool              `json:"internal,omitempty"`
	Attachable bool              `json:"attachable,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// Image is a local image with its content digests and platform.
type Image struct {
	ID           string   `json:"id"`
	RepoTags     []string `json:"repo_tags,omitempty"`
	RepoDigests  []string `json:"repo_digests,omitempty"`
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant,omitempty"`
	Size         int64    `json:"size"`
}

// ComposeProject holds the original Compose definition read from the host
// (hand-deployed projects: files named by the com.docker.compose.* labels).
type ComposeProject struct {
	Name        string   `json:"name"`
	WorkingDir  string   `json:"working_dir"`
	ConfigFiles []File   `json:"config_files,omitempty"`
	EnvFiles    []File   `json:"env_files,omitempty"`
	Errors      []string `json:"errors,omitempty"`
}

// File is a file read from the host. The server replaces sensitive values in
// Content with a mask and keeps the original in Sealed.
type File struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
	Masked    bool   `json:"masked,omitempty"`
	Sealed    []byte `json:"sealed,omitempty"`
}

// Compose labels (set by Docker Compose on every container it creates).
const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelEnvFiles    = "com.docker.compose.project.environment_file"
	LabelOneOff      = "com.docker.compose.oneoff"
	LabelVolume      = "com.docker.compose.volume"
	LabelNetwork     = "com.docker.compose.network"
)
