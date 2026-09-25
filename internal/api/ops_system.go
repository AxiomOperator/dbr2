// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/version"
)

// VersionBody reports platform and component versions (ADR-0015).
type VersionBody struct {
	Platform   string            `json:"platform" doc:"DBR² platform version (MAJOR.MINOR.BUGFIX.BUILD)." example:"0.1.0.0"`
	Components map[string]string `json:"components" doc:"Version of every component, keyed by component name."`
}

type versionOutput struct{ Body VersionBody }

// StatusBody is a liveness/readiness result.
type StatusBody struct {
	Status string            `json:"status" enum:"ok,degraded,unavailable" doc:"Overall status."`
	Checks map[string]string `json:"checks,omitempty" doc:"Per-dependency result: ok or an error summary."`
}

type statusOutput struct {
	Status int
	Body   StatusBody
}

func registerSystem(a huma.API, d *Deps) {
	huma.Register(a, public(op("get-version", http.MethodGet, "/api/v1/version", "System",
		"Get platform and component versions",
		"Returns the DBR² platform version and the four-part version of every component (ADR-0015).", "")),
		func(context.Context, *struct{}) (*versionOutput, error) {
			return &versionOutput{Body: VersionBody{Platform: version.Of(version.Platform), Components: version.All()}}, nil
		})

	huma.Register(a, public(op("get-health-live", http.MethodGet, "/api/v1/health/live", "System",
		"Liveness probe", "Returns 200 while the process is running. Does not check dependencies.", "")),
		func(context.Context, *struct{}) (*statusOutput, error) {
			return &statusOutput{Status: http.StatusOK, Body: StatusBody{Status: "ok"}}, nil
		})

	huma.Register(a, public(op("get-health-ready", http.MethodGet, "/api/v1/health/ready", "System",
		"Readiness probe",
		"Checks PostgreSQL (critical), Temporal, Valkey and the platform services configured in "+
			"`DBR2_READY_HTTP_CHECKS` (in Compose: the Caddy edge proxy, dbr2-worker and dbr2-reposerver, "+
			"whose check also reflects Repository storage health). Returns 503 when a critical dependency is "+
			"unavailable; `degraded` when only a non-critical one is.", "", http.StatusServiceUnavailable)),
		func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
			out := &statusOutput{Status: http.StatusOK, Body: StatusBody{Status: "ok", Checks: map[string]string{}}}
			for _, c := range d.Ready {
				cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := c.Check(cctx)
				cancel()
				if err == nil {
					out.Body.Checks[c.Name] = "ok"
					continue
				}
				out.Body.Checks[c.Name] = "unavailable"
				if c.Critical {
					out.Status, out.Body.Status = http.StatusServiceUnavailable, "unavailable"
				} else if out.Body.Status == "ok" {
					out.Body.Status = "degraded"
				}
			}
			return out, nil
		})
}
