// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/rbac"
)

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// op builds a documented operation. perm "" means "any authenticated user".
func op(id, method, path, tag, summary, description string, perm rbac.Permission, errs ...int) huma.Operation {
	o := huma.Operation{
		OperationID: id, Method: method, Path: path, Tags: []string{tag},
		Summary: summary, Description: description,
		Errors: append([]int{http.StatusUnauthorized}, errs...),
	}
	if perm != "" {
		o.Metadata = map[string]any{MetaPermission: string(perm)}
		o.Extensions = map[string]any{ExtPermission: string(perm)}
		o.Errors = append(o.Errors, http.StatusForbidden)
		o.Description += "\n\nRequires permission `" + string(perm) + "`."
	}
	return o
}

// public marks an operation as not requiring authentication.
func public(o huma.Operation) huma.Operation {
	o.Security = []map[string][]string{}
	errs := o.Errors[:0]
	for _, e := range o.Errors {
		if e != http.StatusUnauthorized {
			errs = append(errs, e)
		}
	}
	o.Errors = errs
	return o
}
