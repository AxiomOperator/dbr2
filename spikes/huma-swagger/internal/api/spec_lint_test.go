package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version"
)

// lintSpec returns one problem per violation of the DBR² API documentation
// rules (final_stack.md: every operation has a summary, description, tags,
// operationId and documented error responses).
func lintSpec(spec *huma.OpenAPI) []string {
	var problems []string
	if spec.Info == nil || !version.Valid(spec.Info.Version) {
		problems = append(problems, "info.version must be MAJOR.MINOR.BUGFIX.BUILD")
	}
	for path, item := range spec.Paths {
		for method, op := range map[string]*huma.Operation{
			"GET": item.Get, "PUT": item.Put, "POST": item.Post, "DELETE": item.Delete,
			"PATCH": item.Patch, "HEAD": item.Head, "OPTIONS": item.Options, "TRACE": item.Trace,
		} {
			if op == nil {
				continue
			}
			where := fmt.Sprintf("%s %s", method, path)
			if op.OperationID == "" {
				problems = append(problems, where+": missing operationId")
			}
			if op.Summary == "" {
				problems = append(problems, where+": missing summary")
			}
			if op.Description == "" {
				problems = append(problems, where+": missing description")
			}
			if len(op.Tags) == 0 {
				problems = append(problems, where+": missing tags")
			}
			if requiresAuthSpec(spec, op) && op.Responses["401"] == nil {
				problems = append(problems, where+": secured operation does not document 401")
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func requiresAuthSpec(spec *huma.OpenAPI, op *huma.Operation) bool {
	if op.Security != nil {
		return len(op.Security) > 0
	}
	return len(spec.Security) > 0
}

// TestSpecLint is the CI gate: `go test ./internal/api -run TestSpecLint`.
func TestSpecLint(t *testing.T) {
	version.API = "0.1.0.0" // what CI injects; the default 0.0.0.0 is also valid
	for _, p := range lintSpec(NewAPI(chi.NewMux()).OpenAPI()) {
		t.Error(p)
	}
}

// TestSpecLintCatchesUndocumented proves the gate fails on a bare operation.
func TestSpecLintCatchesUndocumented(t *testing.T) {
	a := NewAPI(chi.NewMux())
	huma.Register(a, huma.Operation{Method: http.MethodGet, Path: "/api/v1/undocumented"},
		func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	got := lintSpec(a.OpenAPI())
	want := []string{
		"GET /api/v1/undocumented: missing description",
		"GET /api/v1/undocumented: missing operationId",
		"GET /api/v1/undocumented: missing summary",
		"GET /api/v1/undocumented: missing tags",
		"GET /api/v1/undocumented: secured operation does not document 401",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("lint problems:\n got %q\nwant %q", got, want)
	}
}
