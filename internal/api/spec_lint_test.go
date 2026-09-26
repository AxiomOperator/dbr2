// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/version"
)

var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

// lintSpec enforces the API documentation rules (final_stack → Control
// Plane): operationId, summary, description, tags, documented 401 on secured
// operations, a valid permission on every x-dbr2-permission, and a four-part
// info.version, and every {param} in a path declared by its operation.
func lintSpec(spec *huma.OpenAPI) []string {
	var problems []string
	if spec.Info == nil || !version.IsValid(spec.Info.Version) {
		problems = append(problems, "info.version must be MAJOR.MINOR.BUGFIX.BUILD")
	}
	valid := map[string]bool{}
	for _, p := range rbac.All {
		valid[string(p)] = true
	}
	for path, item := range spec.Paths {
		for method, o := range map[string]*huma.Operation{
			"GET": item.Get, "PUT": item.Put, "POST": item.Post, "DELETE": item.Delete,
			"PATCH": item.Patch, "HEAD": item.Head, "OPTIONS": item.Options, "TRACE": item.Trace,
		} {
			if o == nil {
				continue
			}
			where := fmt.Sprintf("%s %s", method, path)
			if o.OperationID == "" {
				problems = append(problems, where+": missing operationId")
			}
			if o.Summary == "" {
				problems = append(problems, where+": missing summary")
			}
			if o.Description == "" {
				problems = append(problems, where+": missing description")
			}
			if len(o.Tags) == 0 {
				problems = append(problems, where+": missing tags")
			}
			secured := len(spec.Security) > 0
			if o.Security != nil {
				secured = len(o.Security) > 0
			}
			if secured && o.Responses["401"] == nil {
				problems = append(problems, where+": secured operation does not document 401")
			}
			declared := map[string]bool{}
			for _, prm := range o.Parameters {
				if prm.In == "path" {
					declared[prm.Name] = true
				}
			}
			for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
				if !declared[m[1]] {
					problems = append(problems, where+": path parameter {"+m[1]+"} is not declared (embedded input structs are not read)")
				}
			}
			if perm, ok := o.Extensions[ExtPermission].(string); ok && !valid[perm] {
				problems = append(problems, where+": unknown permission "+perm)
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// TestSpecLint is the CI gate for API documentation completeness.
func TestSpecLint(t *testing.T) {
	for _, p := range lintSpec(NewAPI(chi.NewMux(), &Deps{}).OpenAPI()) {
		t.Error(p)
	}
}

func TestSpecLintCatchesUndocumented(t *testing.T) {
	a := NewAPI(chi.NewMux(), &Deps{})
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

// TestPermissionsDocumentedMatchEnforced checks every permission-guarded
// operation publishes the same permission it enforces.
func TestPermissionsDocumentedMatchEnforced(t *testing.T) {
	spec := NewAPI(chi.NewMux(), &Deps{}).OpenAPI()
	seen := 0
	for _, item := range spec.Paths {
		for _, o := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete} {
			if o == nil {
				continue
			}
			meta, _ := o.Metadata[MetaPermission].(string)
			ext, _ := o.Extensions[ExtPermission].(string)
			if meta != ext {
				t.Errorf("%s: enforced %q but documented %q", o.OperationID, meta, ext)
			}
			if meta != "" {
				seen++
			}
		}
	}
	if seen < 5 {
		t.Fatalf("expected permission-guarded operations, found %d", seen)
	}
}
