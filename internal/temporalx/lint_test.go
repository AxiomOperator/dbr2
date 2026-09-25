// SPDX-License-Identifier: Apache-2.0

package temporalx

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTemporalUsageLint enforces ADR-0005/0011 across the codebase:
//   - TerminateWorkflow is forbidden (termination skips compensation;
//     DBR² offers Cancel only);
//   - client ExecuteWorkflow may only be called from this package, so every
//     application operation goes through StartApplicationOperation.
func TestTemporalUsageLint(t *testing.T) {
	root := "../.."
	terminate := regexp.MustCompile(`\.TerminateWorkflow\(`)
	execute := regexp.MustCompile(`\b[cC]lient\w*\.ExecuteWorkflow\(|\bc\.ExecuteWorkflow\(|\btc\.ExecuteWorkflow\(`)
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "spikes", ".git", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if terminate.Match(src) {
			problems = append(problems, rel+": TerminateWorkflow is forbidden (use cancellation; ADR-0005)")
		}
		if execute.Match(src) && !strings.HasPrefix(rel, "internal/temporalx/") {
			problems = append(problems, rel+": start workflows via temporalx.StartApplicationOperation (ADR-0011)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestApplicationStartOptions(t *testing.T) {
	o := ApplicationStartOptions("app-1", "q")
	if o.ID != "application/app-1" || !o.WorkflowExecutionErrorWhenAlreadyStarted ||
		o.WorkflowRunTimeout != 0 || o.WorkflowExecutionTimeout != 0 {
		t.Fatalf("unsafe start options: %+v", o)
	}
}
