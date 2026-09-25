// Package version holds component versions injected at build time (ADR-0015).
//
// Every value is a 4-part MAJOR.MINOR.BUGFIX.BUILD string. Local builds keep
// the defaults (BUILD = 0); CI overrides them with -ldflags, for example:
//
//	go build -ldflags "\
//	  -X github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version.Platform=0.1.0.42 \
//	  -X github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version.API=0.1.0.0 \
//	  -X github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version.Server=0.1.0.42" \
//	  ./cmd/dbr2-api
package version

import (
	"fmt"
	"regexp"
)

// These are variables (not constants) so the linker can overwrite them.
var (
	// Platform is the overall DBR² release version.
	Platform = "0.0.0.0"
	// API is the `api` contract component version. It becomes the OpenAPI
	// info.version. Contract components always carry BUILD = 0.
	API = "0.0.0.0"
	// Server is the version of this binary.
	Server = "0.0.0.0"
)

var fourPart = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Valid reports whether v is a well-formed MAJOR.MINOR.BUGFIX.BUILD string.
func Valid(v string) bool { return fourPart.MatchString(v) }

// Check returns an error if any injected version is malformed. Call it at
// startup so a bad -ldflags value fails fast instead of reaching the spec.
func Check() error {
	for name, v := range map[string]string{"platform": Platform, "api": API, "server": Server} {
		if !Valid(v) {
			return fmt.Errorf("version: %s version %q is not MAJOR.MINOR.BUGFIX.BUILD", name, v)
		}
	}
	return nil
}
