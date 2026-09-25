// SPDX-License-Identifier: Apache-2.0

// Package runtime abstracts the container engine (final_stack → Runtime
// Abstraction). DockerRuntime is the first implementation; PodmanRuntime can
// follow without changing the inventory model.
package runtime

import (
	"context"

	"github.com/AxiomOperator/dbr2/internal/inventory"
)

// DiscoverOptions tune a discovery.
type DiscoverOptions struct {
	// FilesystemChanges collects writable-layer changes per container for
	// unprotected-data detection (more expensive).
	FilesystemChanges bool
}

// ContainerRuntime is a container engine on the local host.
type ContainerRuntime interface {
	// Name identifies the runtime ("docker").
	Name() string
	// Ping checks that the engine is reachable and returns its version.
	Ping(ctx context.Context) (version string, err error)
	// Discover collects the host inventory.
	Discover(ctx context.Context, opts DiscoverOptions) (*inventory.Inventory, error)
	Close() error
}

// ContainerState is the observed state of one container.
type ContainerState struct {
	ID   string
	Name string
	// running | paused | exited | created | restarting | removing | dead
	State string
}

// ExecResult is the outcome of a command run inside a container.
type ExecResult struct {
	ExitCode int
	// Combined stdout+stderr; only the last maxOutput bytes are kept.
	Output    []byte
	Truncated bool
}

// ContainerControl is implemented by runtimes that can change container
// state and run commands (Phase 4: quiesce, resume, hooks, config capture).
// It is separate from ContainerRuntime so discovery-only runtimes (and test
// fakes) keep working; the agent type-asserts for it.
type ContainerControl interface {
	// InspectState returns the container's current state.
	InspectState(ctx context.Context, id string) (ContainerState, error)
	// InspectRaw returns the engine's raw inspect document (unredacted).
	InspectRaw(ctx context.Context, id string) ([]byte, error)
	Pause(ctx context.Context, id string) error
	Unpause(ctx context.Context, id string) error
	// Stop stops the container using its configured stop timeout.
	Stop(ctx context.Context, id string) error
	Start(ctx context.Context, id string) error
	// Exec runs cmd in the container (no shell) until it exits or ctx ends.
	// A non-zero exit is not an error.
	Exec(ctx context.Context, id string, cmd []string, maxOutput int) (ExecResult, error)
}
