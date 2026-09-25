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
