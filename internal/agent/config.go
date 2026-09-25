// SPDX-License-Identifier: Apache-2.0

// Package agent implements dbr2-agent (ADR-0001, ADR-0006): enrollment, the
// outbound mTLS session to the Agent Gateway, the durable command journal,
// command execution (discovery) and certificate renewal.
package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is the packaged configuration location.
const DefaultConfigPath = "/etc/dbr2/agent.yaml"

// Config is /etc/dbr2/agent.yaml (written by `dbr2-agent enroll`).
type Config struct {
	Server            string        `yaml:"server"` // gateway host:port
	StateDir          string        `yaml:"state_dir"`
	DockerHost        string        `yaml:"docker_host,omitempty"`
	DiscoveryInterval time.Duration `yaml:"discovery_interval"`
	LogLevel          string        `yaml:"log_level"`
}

// Defaults fills unset fields.
func (c *Config) Defaults() {
	if c.StateDir == "" {
		c.StateDir = "/var/lib/dbr2/agent"
	}
	if c.DockerHost == "" {
		c.DockerHost = "unix:///var/run/docker.sock"
	}
	if c.DiscoveryInterval <= 0 {
		c.DiscoveryInterval = 5 * time.Minute
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

// ErrNotEnrolled means no configuration exists yet.
var ErrNotEnrolled = errors.New("agent is not enrolled: run `dbr2-agent enroll --server <host:port> --token <token> --ca-sha256 <fingerprint>`")

// LoadConfig reads the configuration file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Server == "" {
		return nil, fmt.Errorf("%s: server is required", path)
	}
	c.Defaults()
	return &c, nil
}

// Save writes the configuration with mode 0600.
func (c *Config) Save(path string) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append([]byte("# dbr2-agent configuration (written by `dbr2-agent enroll`)\n"), b...), 0o600)
}

// State file names inside StateDir.
const (
	keyFile    = "agent.key"
	certFile   = "agent.crt"
	caFile     = "ca.crt"
	jrnlFile   = "journal.jsonl"
	leasesFile = "leases.json"
	reposDir   = "repositories"
	tmpDir     = "tmp"
)

func (c *Config) path(name string) string { return filepath.Join(c.StateDir, name) }

// writeFileAtomic writes via a temp file + fsync + rename.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
