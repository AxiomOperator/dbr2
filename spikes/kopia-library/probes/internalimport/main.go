//go:build probe

// Probe: can a program outside the kopia module import Kopia's server/ACL packages?
// Run: go build -tags probe ./probes/internalimport   (expected to FAIL)
package main

import (
	_ "github.com/kopia/kopia/internal/acl"
	_ "github.com/kopia/kopia/internal/server"
)

func main() {}
