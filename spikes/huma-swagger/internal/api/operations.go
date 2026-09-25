package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version"
)

// Standard error responses documented on operations. Huma renders them with
// its RFC 9457 ErrorModel (application/problem+json).
var (
	errsRead  = []int{http.StatusUnauthorized, http.StatusForbidden}
	errsWrite = []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity}
)

// op fills the fields every DBR² operation must carry. The lint test
// (spec_lint_test.go) fails if summary/description/tags are missing.
func op(id, method, path, tag, perm, summary, description string, errs []int) huma.Operation {
	o := huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Tags:        []string{tag},
		Summary:     summary,
		Description: description,
		Errors:      errs,
		Metadata:    map[string]any{MetaPermission: perm},
	}
	if perm != "" {
		o.Extensions = map[string]any{"x-dbr2-permission": perm}
	}
	return o
}

// --- System -----------------------------------------------------------------

type VersionOutput struct {
	Body struct {
		Platform   string            `json:"platform" example:"0.1.0.42" doc:"DBR² platform release (MAJOR.MINOR.BUGFIX.BUILD)."`
		Components map[string]string `json:"components" doc:"Version of each component, keyed by component name."`
	}
}

func registerSystem(api huma.API) {
	huma.Register(api, op("get-version", http.MethodGet, "/api/v1/version", "System", "",
		"Get platform and component versions",
		"Returns the DBR² platform version and the version of each component (ADR-0015). "+
			"The `api` component version is also the OpenAPI `info.version`.",
		errsRead),
		func(ctx context.Context, _ *struct{}) (*VersionOutput, error) {
			out := &VersionOutput{}
			out.Body.Platform = version.Platform
			out.Body.Components = map[string]string{"api": version.API, "server": version.Server}
			return out, nil
		})
}

// --- Applications -------------------------------------------------------------

type Application struct {
	ID         string    `json:"id" format:"uuid" example:"0b8f3c2e-3f7a-4b53-9c1e-1f2a3b4c5d6e" doc:"Application ID."`
	Name       string    `json:"name" example:"wiki" doc:"Compose project or stack name."`
	Host       string    `json:"host" example:"docker-01" doc:"Docker host the application runs on."`
	Containers int       `json:"containers" minimum:"0" example:"3" doc:"Number of containers."`
	LastBackup time.Time `json:"last_backup,omitzero" doc:"Time of the last successful backup, if any."`
}

type ListApplicationsInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"Maximum number of items to return."`
	Cursor string `query:"cursor" doc:"Opaque cursor from a previous response's next_cursor."`
}

type ListApplicationsOutput struct {
	Body struct {
		Items      []Application `json:"items" doc:"Applications visible to the caller."`
		NextCursor string        `json:"next_cursor,omitempty" doc:"Cursor for the next page; absent on the last page."`
	}
}

func registerApplications(api huma.API) {
	huma.Register(api, op("list-applications", http.MethodGet, "/api/v1/applications", "Applications", "applications:read",
		"List applications",
		"Lists the Docker applications discovered by the agents that the caller is allowed to see. Results are paginated with an opaque cursor.",
		errsRead),
		func(ctx context.Context, in *ListApplicationsInput) (*ListApplicationsOutput, error) {
			out := &ListApplicationsOutput{}
			out.Body.Items = []Application{{
				ID: "0b8f3c2e-3f7a-4b53-9c1e-1f2a3b4c5d6e", Name: "wiki", Host: "docker-01", Containers: 3,
			}}
			return out, nil
		})
}

// --- Backups ------------------------------------------------------------------

type CreateBackupBody struct {
	ApplicationID  string `json:"application_id" format:"uuid" example:"0b8f3c2e-3f7a-4b53-9c1e-1f2a3b4c5d6e" doc:"Application to back up."`
	Reason         string `json:"reason,omitempty" maxLength:"500" example:"pre-upgrade snapshot" doc:"Free-text reason recorded in the audit log."`
	IncludeVolumes bool   `json:"include_volumes,omitempty" default:"true" doc:"Back up named volumes and bind mounts."`
}

type CreateBackupInput struct {
	Body CreateBackupBody
}

type Backup struct {
	ID            string    `json:"id" format:"uuid" example:"7d1e8a90-5c4b-4f1e-8a2d-3b4c5d6e7f80" doc:"Backup run ID."`
	ApplicationID string    `json:"application_id" format:"uuid" doc:"Application being backed up."`
	Status        string    `json:"status" enum:"queued,running,succeeded,failed" example:"queued" doc:"Run status."`
	CreatedAt     time.Time `json:"created_at" doc:"When the run was requested."`
}

type CreateBackupOutput struct {
	Body Backup
}

func registerBackups(api huma.API) {
	o := op("create-backup", http.MethodPost, "/api/v1/backups", "Backups", "backups:create",
		"Start a backup",
		"Queues an on-demand backup run for an application. The run executes asynchronously as a Temporal workflow; poll the returned backup to follow progress.",
		errsWrite)
	o.DefaultStatus = http.StatusAccepted
	huma.Register(api, o, func(ctx context.Context, in *CreateBackupInput) (*CreateBackupOutput, error) {
		out := &CreateBackupOutput{}
		out.Body = Backup{
			ID: "7d1e8a90-5c4b-4f1e-8a2d-3b4c5d6e7f80", ApplicationID: in.Body.ApplicationID,
			Status: "queued", CreatedAt: time.Now().UTC(),
		}
		return out, nil
	})
}
