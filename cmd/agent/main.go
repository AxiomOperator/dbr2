// SPDX-License-Identifier: Apache-2.0

// Command dbr2-agent is the DBR² data-plane agent (component `agent`,
// ADR-0006): a native systemd service on each protected Docker host. Phase 1
// ships only version reporting; enrollment, the Agent Gateway connection and
// discovery arrive in Phases 2–3 (see docs/roadmap.md).
package main

import (
	"fmt"
	"os"

	"github.com/AxiomOperator/dbr2/internal/version"
)

const binary = "dbr2-agent"

func main() {
	if err := version.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(version.String(binary, version.Agent))
		fmt.Printf("agent-protocol %s\n", version.Of(version.AgentProtocol))
		return
	}
	fmt.Fprintf(os.Stderr, "%s: not yet implemented (Phase 2: enrollment and Agent Gateway). Usage: %s version\n", binary, binary)
	os.Exit(2)
}
