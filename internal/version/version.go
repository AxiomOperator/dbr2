// SPDX-License-Identifier: Apache-2.0

// Package version exposes DBR² platform and component versions in the
// MAJOR.MINOR.BUGFIX.BUILD form required by ADR-0015.
//
// MAJOR.MINOR.BUGFIX comes from each component's VERSION file (compiled into
// components_gen.go by `go generate`). BUILD is injected at link time:
//
//	go build -ldflags "-X github.com/AxiomOperator/dbr2/internal/version.Build=123"
//
// Local builds default to BUILD 0. Contract components (the API, the agent
// protocol and the schemas) are not built artifacts, so their BUILD is always 0.
package version

//go:generate go run ./gen -root ../.. -out components_gen.go

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// Build is the CI build number (GitHub Actions run_number). Set via -ldflags.
var Build = "0"

// Component names (ADR-0015).
const (
	Platform       = "platform"
	API            = "api"
	Server         = "server"
	Worker         = "worker"
	Agent          = "agent"
	RepoServer     = "reposerver"
	CLI            = "cli"
	Web            = "web"
	AgentProtocol  = "agent-protocol"
	ManifestSchema = "manifest-schema"
	DBSchema       = "db-schema"
	Deployment     = "deployment"
)

// contractComponents never carry a build number.
var contractComponents = map[string]bool{
	API: true, AgentProtocol: true, ManifestSchema: true, DBSchema: true,
}

var (
	threePart = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)
	fourPart  = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)
)

// Validate checks the link-time build number and every generated version.
// Binaries call it at startup and refuse to run on malformed values.
func Validate() error {
	if _, err := strconv.ParseUint(Build, 10, 64); err != nil {
		return fmt.Errorf("version: invalid build number %q", Build)
	}
	for name, v := range components {
		if !threePart.MatchString(v) {
			return fmt.Errorf("version: component %s has invalid VERSION %q", name, v)
		}
	}
	return nil
}

// Of returns the full four-part version of a component (or the platform).
func Of(component string) string {
	base, ok := components[component]
	if !ok {
		return "0.0.0.0"
	}
	if contractComponents[component] {
		return base + ".0"
	}
	return base + "." + Build
}

// All returns the four-part version of every component, keyed by name,
// excluding the platform itself.
func All() map[string]string {
	out := make(map[string]string, len(components))
	for name := range components {
		if name != Platform {
			out[name] = Of(name)
		}
	}
	return out
}

// Names returns the sorted component names (excluding the platform).
func Names() []string {
	var n []string
	for name := range components {
		if name != Platform {
			n = append(n, name)
		}
	}
	sort.Strings(n)
	return n
}

// IsValid reports whether v is a well-formed four-part version.
func IsValid(v string) bool { return fourPart.MatchString(v) }

// String renders "<binary> <version> (platform <version>)" for --version.
func String(binary, component string) string {
	return fmt.Sprintf("%s %s (DBR² platform %s)", binary, Of(component), Of(Platform))
}
