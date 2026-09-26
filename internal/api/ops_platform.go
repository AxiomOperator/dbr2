// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/protection"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// PlatformBackupDTO is one Platform Protection run (ADR-0008).
type PlatformBackupDTO struct {
	ID           string          `json:"id"`
	State        string          `json:"state" enum:"running,succeeded,partial,failed"`
	Trigger      string          `json:"trigger"`
	WorkflowID   *string         `json:"workflow_id"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	SizeBytes    int64           `json:"size_bytes" doc:"Size of the encrypted bundle."`
	SHA256       *string         `json:"sha256" doc:"SHA-256 (hex) of the encrypted bundle file."`
	FileName     *string         `json:"file_name" doc:"dbr2-platform-<UTC timestamp>.tar.zst.age"`
	SnapshotID   *string         `json:"snapshot_id" doc:"Pinned Kopia snapshot in the System Repository (null = not written there)."`
	RepositoryID *string         `json:"repository_id" format:"uuid" doc:"The System Repository at the time of the run."`
	BundlePath   *string         `json:"bundle_path" doc:"Copy in the worker's bundle directory (null = not written there)."`
	Error        *string         `json:"error"`
	Manifest     json.RawMessage `json:"manifest,omitempty" doc:"The bundle's plaintext manifest.json (versions, row counts, digests, missing reposervers). Never contains secrets."`
}

func platformBackupDTO(b store.PlatformBackup) PlatformBackupDTO {
	d := PlatformBackupDTO{ID: b.ID, State: b.State, Trigger: b.Trigger, WorkflowID: b.WorkflowID, StartedAt: b.StartedAt,
		FinishedAt: b.FinishedAt, SizeBytes: b.SizeBytes, SHA256: b.Sha256, FileName: b.FileName, SnapshotID: b.SnapshotID,
		BundlePath: b.BundlePath, Error: b.Error, Manifest: b.Manifest}
	if b.RepositoryID != nil {
		s := b.RepositoryID.String()
		d.RepositoryID = &s
	}
	return d
}

func registerPlatform(a huma.API, d *Deps) {
	const tag = "Platform protection"

	huma.Register(a, op("list-platform-backups", http.MethodGet, "/api/v1/platform/backups", tag,
		"List platform backups", "Platform Protection runs (ADR-0008), newest first: the age-encrypted Platform Recovery Bundle written to the "+
			"System Repository and to the bundle directory outside every Repository. Partial means one target failed or a reposerver's "+
			"state could not be exported.", rbac.RepositoryManage),
		func(ctx context.Context, in *struct {
			Limit int32 `query:"limit" minimum:"1" maximum:"500" default:"50"`
		}) (*struct {
			Body struct {
				Items []PlatformBackupDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Protection.ListPlatformBackups(ctx, in.Limit)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []PlatformBackupDTO `json:"items"`
				}
			}{}
			out.Body.Items = []PlatformBackupDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, platformBackupDTO(r))
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("start-platform-backup", http.MethodPost, "/api/v1/platform/backups", tag,
		"Back up the platform now", "Starts the Platform Protection workflow (one run at a time: 409 while one runs). The bundle holds "+
			"the platform database, DBR2_SECRET_KEY, the internal token and every reposerver's state, encrypted to the escrow recipients.",
		rbac.RepositoryManage, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, _ *struct{}) (*workflowOut, error) {
			wf, err := d.Protection.StartPlatformBackup(ctx, principal(ctx), metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return wfOut(wf), nil
		})

	huma.Register(a, op("designate-system-repository", http.MethodPut, "/api/v1/repositories/{id}/system", "Repositories",
		"Designate the System Repository", "Makes this Repository the System Repository that receives the platform self-backup "+
			"(ADR-0008). At most one Repository is the System Repository; the previous one loses the role. The bundle is also written "+
			"to a directory outside every Repository, so the System Repository is never the only copy.",
		rbac.RepositoryManage, http.StatusBadRequest, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body RepositoryDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := d.Protection.SetSystemRepository(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RepositoryDTO }{repositoryDTO(protection.RepositoryView{Repository: r})}, nil
		})
}
