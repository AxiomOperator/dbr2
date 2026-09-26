// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/protection"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
)

// EscrowRecipientDTO is an escrow recipient (public key only).
type EscrowRecipientDTO struct {
	ID        string    `json:"id" format:"uuid"`
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
}

// RepositoryDTO is a Repository with live reposerver status.
type RepositoryDTO struct {
	ID                string         `json:"id" format:"uuid"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Backend           string         `json:"backend" enum:"nfs,filesystem"`
	Status            string         `json:"status" enum:"awaiting_escrow,ready,unavailable,pending_deletion,retired"`
	IsDefault         bool           `json:"is_default"`
	IsSystem          bool           `json:"is_system" doc:"The System Repository that receives Platform Recovery Bundles (ADR-0008)."`
	ServerURL         string         `json:"server_url"`
	InternalServerURL string         `json:"internal_server_url"`
	ManagementURL     string         `json:"management_url"`
	CertSHA256        string         `json:"cert_sha256" doc:"Pinned reposerver certificate fingerprint."`
	KopiaRepositoryID *string        `json:"kopia_repository_id"`
	Splitter          *string        `json:"splitter"`
	EscrowRecipients  int            `json:"escrow_recipients"`
	EscrowGeneratedAt *time.Time     `json:"escrow_generated_at"`
	EscrowConfirmedAt *time.Time     `json:"escrow_confirmed_at"`
	LastReindexAt     *time.Time     `json:"last_reindex_at"`
	LastVerifiedAt    *time.Time     `json:"last_verified_at"`
	DeleteAfter       *time.Time     `json:"delete_after" doc:"Set while the Repository is pending deletion (grace period)."`
	DeleteReason      *string        `json:"delete_reason"`
	CreatedAt         time.Time      `json:"created_at"`
	Live              *LiveDTO       `json:"live" doc:"Live reposerver status; null when unreachable (see live_error)."`
	LiveError         string         `json:"live_error,omitempty"`
	UsageByHost       []HostUsageDTO `json:"usage_by_host" doc:"Per-host logical size of each application's latest recovery point (deduplicated physical usage is shared and not attributable)."`
}

// HostUsageDTO is one host's share of a Repository.
type HostUsageDTO struct {
	HostID       string `json:"host_id" format:"uuid"`
	Hostname     string `json:"hostname"`
	Applications int32  `json:"applications"`
	LatestBytes  int64  `json:"latest_bytes"`
}

// LiveDTO is the reposerver's live status.
type LiveDTO struct {
	Initialized    bool   `json:"initialized"`
	ServerRunning  bool   `json:"server_running"`
	KopiaVersion   string `json:"kopia_version"`
	StorageHealthy bool   `json:"storage_healthy"`
	StorageError   string `json:"storage_error,omitempty"`
	StorageTotal   uint64 `json:"storage_total_bytes"`
	StorageFree    uint64 `json:"storage_free_bytes"`
	StorageUsed    uint64 `json:"storage_used_bytes"`
}

func repositoryDTO(v protection.RepositoryView) RepositoryDTO {
	r := v.Repository
	d := RepositoryDTO{ID: r.ID.String(), Name: r.Name, Description: r.Description, Backend: r.Backend, Status: r.Status, IsDefault: r.IsDefault,
		ServerURL: r.ServerUrl, InternalServerURL: r.InternalServerUrl, ManagementURL: r.ManagementUrl, CertSHA256: r.CertSha256, KopiaRepositoryID: r.KopiaRepositoryID,
		Splitter: r.Splitter, EscrowRecipients: len(r.EscrowRecipientIds), EscrowGeneratedAt: r.EscrowGeneratedAt,
		EscrowConfirmedAt: r.EscrowConfirmedAt, LastReindexAt: r.LastReindexAt, CreatedAt: r.CreatedAt, LiveError: v.LiveError,
		LastVerifiedAt: r.LastVerifiedAt, DeleteAfter: r.DeleteAfter, IsSystem: r.IsSystem, DeleteReason: r.DeleteReason,
		UsageByHost: []HostUsageDTO{}}
	for _, u := range v.Usage {
		d.UsageByHost = append(d.UsageByHost, HostUsageDTO{HostID: u.AgentID.String(), Hostname: u.Hostname, Applications: u.Applications, LatestBytes: u.LatestBytes})
	}
	if l := v.Live; l != nil {
		d.Live = &LiveDTO{Initialized: l.Initialized, ServerRunning: l.ServerRunning, KopiaVersion: l.KopiaVersion, StorageHealthy: l.StorageHealthy,
			StorageError: l.StorageError, StorageTotal: l.StorageTotal, StorageFree: l.StorageFree, StorageUsed: l.StorageUsed}
	}
	return d
}

// HookDTO is a pre/post-backup hook.
type HookDTO struct {
	Container      string   `json:"container" minLength:"1" doc:"Service name, container name or container ID of the application."`
	Command        []string `json:"command" minItems:"1" doc:"Executed with docker exec (no shell unless the command starts one)."`
	TimeoutSeconds uint32   `json:"timeout_seconds,omitempty" maximum:"3600"`
	Optional       bool     `json:"optional,omitempty" doc:"A failing optional hook does not fail the backup."`
}

// BackupSettingsDTO is an application's backup settings.
type BackupSettingsDTO struct {
	RepositoryID       *string    `json:"repository_id" format:"uuid" doc:"Null = the default Repository."`
	ConsistencyMode    *string    `json:"consistency_mode" enum:"live,quiesced,offline" doc:"Null = automatic: quiesced when hooks are defined, otherwise live (crash-consistent)."`
	EffectiveMode      string     `json:"effective_mode,omitempty" readOnly:"true"`
	MaxQuiesceSeconds  int32      `json:"max_quiesce_seconds" minimum:"60" maximum:"86400" doc:"Upper bound on quiesce; the agent dead-man switch resumes the application 10 minutes later."`
	PreHooks           []HookDTO  `json:"pre_hooks"`
	PostHooks          []HookDTO  `json:"post_hooks"`
	OptionalComponents []string   `json:"optional_components" doc:"Best-effort components (e.g. volume:cache); their failure makes the recovery point Partial."`
	ExcludedComponents []string   `json:"excluded_components"`
	DatabaseStrategy   string     `json:"database_strategy,omitempty" enum:"logical,volume,both" doc:"Detected PostgreSQL/Redis containers: logical (dumps only; their data volumes are skipped), volume (no dumps) or both (default)."`
	UpdatedAt          *time.Time `json:"updated_at,omitempty" readOnly:"true"`
}

func hooksDTO(hs []protection.HookSpec) []HookDTO {
	out := []HookDTO{}
	for _, h := range hs {
		out = append(out, HookDTO(h))
	}
	return out
}

func hooksIn(hs []HookDTO) []protection.HookSpec {
	var out []protection.HookSpec
	for _, h := range hs {
		out = append(out, protection.HookSpec(h))
	}
	return out
}

func settingsDTO(b protection.BackupSettings) BackupSettingsDTO {
	d := BackupSettingsDTO{ConsistencyMode: b.ConsistencyMode, EffectiveMode: b.EffectiveMode(), MaxQuiesceSeconds: b.MaxQuiesceSeconds,
		PreHooks: hooksDTO(b.PreHooks), PostHooks: hooksDTO(b.PostHooks), OptionalComponents: nonNilStrings(b.OptionalComponents),
		ExcludedComponents: nonNilStrings(b.ExcludedComponents), UpdatedAt: b.UpdatedAt, DatabaseStrategy: b.DatabaseStrategy}
	if b.RepositoryID != nil {
		s := b.RepositoryID.String()
		d.RepositoryID = &s
	}
	return d
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// HostSettingsDTO are a host's limits.
type HostSettingsDTO struct {
	MaxConcurrentJobs int32  `json:"max_concurrent_jobs" minimum:"1" maximum:"16" doc:"Snapshot/restore jobs the agent runs at once."`
	WindowStart       *int32 `json:"backup_window_start" minimum:"0" maximum:"1439" doc:"Minute of the day scheduled backups may start (null = always)."`
	WindowEnd         *int32 `json:"backup_window_end" minimum:"0" maximum:"1439" doc:"Minute of the day the window closes; may wrap midnight."`
	Timezone          string `json:"backup_window_timezone" doc:"IANA timezone, e.g. America/Chicago."`
}

// RecoveryPointDTO is an indexed recovery point.
type RecoveryPointDTO struct {
	ID                  string          `json:"id"`
	ApplicationID       string          `json:"application_id" format:"uuid"`
	ApplicationName     string          `json:"application_name"`
	HostID              string          `json:"host_id" format:"uuid"`
	Hostname            string          `json:"hostname"`
	RepositoryID        string          `json:"repository_id" format:"uuid"`
	State               string          `json:"state" enum:"pending,committed,failed,missing,deleting,deleted"`
	Status              *string         `json:"status" enum:"complete,partial"`
	Verification        string          `json:"verification" enum:"unverified,verified,verification_failed"`
	ConsistencyMode     string          `json:"consistency_mode" enum:"live,quiesced,offline"`
	ConsistencyPoint    *time.Time      `json:"consistency_point"`
	CrashConsistentOnly bool            `json:"crash_consistent_only"`
	Trigger             string          `json:"trigger"`
	WorkflowID          string          `json:"workflow_id"`
	SizeBytes           int64           `json:"size_bytes"`
	ComponentCount      int32           `json:"component_count"`
	Error               *string         `json:"error"`
	CreatedAt           time.Time       `json:"created_at"`
	CommittedAt         *time.Time      `json:"committed_at"`
	Manifest            json.RawMessage `json:"manifest,omitempty" doc:"Recovery manifest (schema_version 1); detail view only."`
	VerifiedAt          *time.Time      `json:"verified_at"`
	VerificationDetails json.RawMessage `json:"verification_details,omitempty" doc:"Per-component verification results (detail view)."`
	DeleteAfter         *time.Time      `json:"delete_after" doc:"Scheduled deletion time (grace period); null when not scheduled."`
	DeleteReason        *string         `json:"delete_reason"`
	DeletedAt           *time.Time      `json:"deleted_at"`
}

func rpDTO(r store.RecoveryPoint, withManifest bool) RecoveryPointDTO {
	d := RecoveryPointDTO{ID: r.ID, ApplicationID: r.ApplicationID.String(), ApplicationName: r.ApplicationName, HostID: r.AgentID.String(),
		Hostname: r.Hostname, RepositoryID: r.RepositoryID.String(), State: r.State, Status: r.Status, Verification: r.Verification,
		ConsistencyMode: r.ConsistencyMode, ConsistencyPoint: r.ConsistencyPoint, CrashConsistentOnly: r.CrashConsistentOnly,
		Trigger: r.Trigger, WorkflowID: r.WorkflowID, SizeBytes: r.SizeBytes, ComponentCount: r.ComponentCount, Error: r.Error,
		CreatedAt: r.CreatedAt, CommittedAt: r.CommittedAt}
	d.VerifiedAt, d.DeleteAfter, d.DeleteReason, d.DeletedAt = r.VerifiedAt, r.DeleteAfter, r.DeleteReason, r.DeletedAt
	if withManifest {
		d.Manifest = r.Manifest
		d.VerificationDetails = r.VerificationDetails
	}
	return d
}

// AlertDTO is an alert.
type AlertDTO struct {
	ID             int64           `json:"id"`
	Severity       string          `json:"severity" enum:"critical,warning,info"`
	Type           string          `json:"type"`
	TargetType     *string         `json:"target_type"`
	TargetID       *string         `json:"target_id"`
	Message        string          `json:"message"`
	Details        json.RawMessage `json:"details"`
	CreatedAt      time.Time       `json:"created_at"`
	AcknowledgedAt *time.Time      `json:"acknowledged_at"`
}

func (d *Deps) protErr(ctx context.Context, err error) error {
	var busy *temporalx.ErrOperationInProgress
	switch {
	case err == nil:
		return nil
	case errors.Is(err, protection.ErrNotFound), errors.Is(err, fleet.ErrNotFound):
		return problem(http.StatusNotFound, CodeNotFound, "not found")
	case errors.Is(err, protection.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, err.Error())
	case errors.Is(err, protection.ErrInvalid):
		return problem(http.StatusBadRequest, CodeValidation, err.Error())
	case errors.Is(err, protection.ErrConflict), errors.As(err, &busy):
		return problem(http.StatusConflict, CodeConflict, err.Error())
	}
	return d.fail(ctx, err)
}

type workflowOut struct {
	Body struct {
		WorkflowID string `json:"workflow_id"`
	}
}

func wfOut(id string) *workflowOut {
	o := &workflowOut{}
	o.Body.WorkflowID = id
	return o
}

func registerProtection(a huma.API, d *Deps) {
	const repoTag, backupTag = "Repositories", "Backups"

	// ---- escrow recipients ----
	huma.Register(a, op("list-escrow-recipients", http.MethodGet, "/api/v1/escrow/recipients", repoTag,
		"List escrow recipients", "age public keys every Repository password is sealed to (ADR-0008). Private identities are kept offline and never uploaded.", rbac.RepositoryRead),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Items []EscrowRecipientDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Protection.ListEscrowRecipients(ctx)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []EscrowRecipientDTO `json:"items"`
				}
			}{}
			out.Body.Items = []EscrowRecipientDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, EscrowRecipientDTO{ID: r.ID.String(), Name: r.Name, PublicKey: r.PublicKey, CreatedAt: r.CreatedAt})
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("add-escrow-recipient", http.MethodPost, "/api/v1/escrow/recipients", repoTag,
		"Add an escrow recipient", "Registers an age X25519 public key (age1…) or an SSH ed25519/RSA public key. v1.0 expects two recipients held by two people.",
		rbac.RepositoryManage, http.StatusBadRequest, http.StatusConflict), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name      string `json:"name" minLength:"1" maxLength:"100"`
				PublicKey string `json:"public_key" minLength:"1" maxLength:"2000"`
			}
		}) (*struct{ Body EscrowRecipientDTO }, error) {
			r, err := d.Protection.AddEscrowRecipient(ctx, principal(ctx), in.Body.Name, in.Body.PublicKey, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body EscrowRecipientDTO }{EscrowRecipientDTO{ID: r.ID.String(), Name: r.Name, PublicKey: r.PublicKey, CreatedAt: r.CreatedAt}}, nil
		})

	huma.Register(a, withStatus(op("remove-escrow-recipient", http.MethodDelete, "/api/v1/escrow/recipients/{id}", repoTag,
		"Remove an escrow recipient", "New escrow packages are no longer encrypted to it; existing packages are unchanged.", rbac.RepositoryManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, d.protErr(ctx, d.Protection.RemoveEscrowRecipient(ctx, principal(ctx), id, metaFrom(ctx)))
		})

	// ---- repositories ----
	huma.Register(a, op("list-repositories", http.MethodGet, "/api/v1/repositories", repoTag,
		"List Repositories", "Every Repository with its escrow state and the live status of its reposerver (storage health and capacity).", rbac.RepositoryRead),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Items []RepositoryDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Protection.ListRepositories(ctx)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []RepositoryDTO `json:"items"`
				}
			}{}
			out.Body.Items = []RepositoryDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, repositoryDTO(r))
			}
			return out, nil
		})

	huma.Register(a, op("get-repository", http.MethodGet, "/api/v1/repositories/{id}", repoTag,
		"Get a Repository", "", rbac.RepositoryRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body RepositoryDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := d.Protection.GetRepository(ctx, id)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RepositoryDTO }{repositoryDTO(r)}, nil
		})

	type createdRepo struct {
		Body struct {
			Repository     RepositoryDTO `json:"repository"`
			EscrowPackage  string        `json:"escrow_package" doc:"ASCII-armored age file holding the repository password and a confirmation code. Store it offline, then confirm with the code."`
			EscrowFilename string        `json:"escrow_filename"`
		}
	}
	huma.Register(a, withStatus(op("create-repository", http.MethodPost, "/api/v1/repositories", repoTag,
		"Create a Repository",
		"Initializes the Kopia repository on an empty reposerver with a generated password, seals the password to every escrow recipient and returns the "+
			"escrow package. The Repository stays `awaiting_escrow` (unusable) until the confirmation code from the decrypted package is entered (ADR-0008). "+
			"DBR² does not store the repository password.",
		rbac.RepositoryManage, http.StatusBadRequest, http.StatusConflict), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name          string `json:"name" minLength:"1" maxLength:"100"`
				Description   string `json:"description,omitempty" maxLength:"500"`
				Backend       string `json:"backend" enum:"nfs,filesystem"`
				ManagementURL string `json:"management_url" doc:"Reposerver management API, e.g. http://dbr2-reposerver:8091"`
				ServerURL     string `json:"server_url" doc:"Kopia repository server URL agents connect to, e.g. https://backup.example.lan:51515"`
				InternalURL   string `json:"internal_server_url,omitempty" doc:"Kopia server URL for dbr2-worker on the deployment network, e.g. https://dbr2-reposerver:51515 (default: server_url)."`
				Default       bool   `json:"default,omitempty"`
			}
		}) (*createdRepo, error) {
			c, err := d.Protection.CreateRepository(ctx, principal(ctx), protection.CreateRepositoryInput{Name: in.Body.Name, Description: in.Body.Description,
				Backend: in.Body.Backend, ManagementURL: in.Body.ManagementURL, ServerURL: in.Body.ServerURL, InternalServerURL: in.Body.InternalURL, Default: in.Body.Default}, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &createdRepo{}
			out.Body.Repository = repositoryDTO(protection.RepositoryView{Repository: c.Repository})
			out.Body.EscrowPackage = string(c.EscrowPackage)
			out.Body.EscrowFilename = "dbr2-escrow-repository-" + c.Repository.Name + ".age"
			return out, nil
		})

	huma.Register(a, op("get-repository-escrow-package", http.MethodGet, "/api/v1/repositories/{id}/escrow-package", repoTag,
		"Download the escrow package", "The package is encrypted to the escrow recipients' public keys; downloading it is audited.", rbac.RepositoryManage, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct {
			Body struct {
				Filename string `json:"filename"`
				Package  string `json:"package"`
			}
		}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			pkg, name, err := d.Protection.EscrowPackage(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Filename string `json:"filename"`
					Package  string `json:"package"`
				}
			}{}
			out.Body.Filename, out.Body.Package = name, string(pkg)
			return out, nil
		})

	huma.Register(a, op("confirm-repository-escrow", http.MethodPost, "/api/v1/repositories/{id}/escrow/confirm", repoTag,
		"Confirm key escrow", "Enter the confirmation code from the decrypted escrow package (`age -d -i <identity> <file>`). This proves the package can be "+
			"decrypted and makes the Repository ready.", rbac.RepositoryManage, http.StatusBadRequest, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body struct {
				ConfirmationCode string `json:"confirmation_code" minLength:"16" maxLength:"40"`
			}
		}) (*struct{ Body RepositoryDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := d.Protection.ConfirmEscrow(ctx, principal(ctx), id, in.Body.ConfirmationCode, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RepositoryDTO }{repositoryDTO(protection.RepositoryView{Repository: r})}, nil
		})

	huma.Register(a, withStatus(op("reindex-repository", http.MethodPost, "/api/v1/repositories/{id}/reindex", repoTag,
		"Rebuild the recovery-point index", "Starts the reindex workflow: every manifest written by maint@dbr2 is read from the Repository and the index is "+
			"replaced (the Repository wins; ADR-0003). Index rows without a manifest are marked missing.", rbac.RepositoryManage, http.StatusNotFound, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, in *idPath) (*workflowOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			wf, err := d.Protection.StartReindex(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return wfOut(wf), nil
		})

	// ---- backups ----
	huma.Register(a, withStatus(op("start-backup", http.MethodPost, "/api/v1/applications/{id}/backups", backupTag,
		"Back up now", "Starts a backup workflow (one operation per application at a time: 409 while one runs).", rbac.BackupExecute,
		http.StatusBadRequest, http.StatusNotFound, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body *struct {
				ConsistencyMode string `json:"consistency_mode,omitempty" enum:"live,quiesced,offline" doc:"Override for this run."`
			} `required:"false"`
		}) (*workflowOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			mode := ""
			if in.Body != nil {
				mode = in.Body.ConsistencyMode
			}
			wf, err := d.Protection.StartBackup(ctx, principal(ctx), id, mode, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return wfOut(wf), nil
		})

	huma.Register(a, op("get-backup-settings", http.MethodGet, "/api/v1/applications/{id}/backup-settings", backupTag,
		"Get backup settings", "", rbac.PolicyRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body BackupSettingsDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			b, err := d.Protection.GetBackupSettings(ctx, id)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body BackupSettingsDTO }{settingsDTO(b)}, nil
		})

	huma.Register(a, op("put-backup-settings", http.MethodPut, "/api/v1/applications/{id}/backup-settings", backupTag,
		"Set backup settings", "Consistency mode, maximum quiesce, hooks, optional and excluded components. Audited with before/after.", rbac.PolicyManage,
		http.StatusBadRequest, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body BackupSettingsDTO
		}) (*struct{ Body BackupSettingsDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			b := protection.BackupSettings{ConsistencyMode: in.Body.ConsistencyMode, MaxQuiesceSeconds: in.Body.MaxQuiesceSeconds,
				PreHooks: hooksIn(in.Body.PreHooks), PostHooks: hooksIn(in.Body.PostHooks), OptionalComponents: in.Body.OptionalComponents,
				ExcludedComponents: in.Body.ExcludedComponents, DatabaseStrategy: in.Body.DatabaseStrategy}
			if in.Body.RepositoryID != nil {
				r, err := uuid.Parse(*in.Body.RepositoryID)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("invalid repository_id")
				}
				b.RepositoryID = &r
			}
			out, err := d.Protection.PutBackupSettings(ctx, principal(ctx), id, b, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body BackupSettingsDTO }{settingsDTO(out)}, nil
		})

	huma.Register(a, op("get-host-settings", http.MethodGet, "/api/v1/agents/{id}/settings", "Hosts",
		"Get host limits", "", rbac.HostRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body HostSettingsDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			h, err := d.Protection.GetHostSettings(ctx, id)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body HostSettingsDTO }{HostSettingsDTO(h)}, nil
		})

	huma.Register(a, op("put-host-settings", http.MethodPut, "/api/v1/agents/{id}/settings", "Hosts",
		"Set host limits", "Maximum concurrent jobs and the backup window scheduled backups wait for (e.g. outside Veeam job windows). "+
			"The agent reconnects to apply a new concurrency limit.", rbac.HostManage, http.StatusBadRequest, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body HostSettingsDTO
		}) (*struct{ Body HostSettingsDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			h, err := d.Protection.PutHostSettings(ctx, principal(ctx), id, protection.HostSettings(in.Body), metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body HostSettingsDTO }{HostSettingsDTO(h)}, nil
		})

	huma.Register(a, op("list-recovery-points", http.MethodGet, "/api/v1/recovery-points", backupTag,
		"List recovery points", "The recovery-point index, newest first. A recovery point exists if and only if its manifest exists in the Repository (ADR-0004).", rbac.BackupRead),
		func(ctx context.Context, in *struct {
			ApplicationID string `query:"application_id" format:"uuid"`
			State         string `query:"state" enum:"pending,committed,failed,missing,deleting,deleted"`
			Limit         int32  `query:"limit" minimum:"1" maximum:"500" default:"100"`
		}) (*struct {
			Body struct {
				Items []RecoveryPointDTO `json:"items"`
			}
		}, error) {
			f := protection.RecoveryPointFilter{Limit: in.Limit}
			if in.ApplicationID != "" {
				id, err := parseID(in.ApplicationID)
				if err != nil {
					return nil, err
				}
				f.ApplicationID = &id
			}
			if in.State != "" {
				f.State = &in.State
			}
			rows, err := d.Protection.ListRecoveryPoints(ctx, f)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []RecoveryPointDTO `json:"items"`
				}
			}{}
			out.Body.Items = []RecoveryPointDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, rpDTO(r, false))
			}
			return out, nil
		})

	huma.Register(a, op("get-recovery-point", http.MethodGet, "/api/v1/recovery-points/{id}", backupTag,
		"Get a recovery point", "Includes the recovery manifest.", rbac.BackupRead, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID string `path:"id" pattern:"^rp_[0-9A-HJKMNP-TV-Z]{26}$"`
		}) (*struct{ Body RecoveryPointDTO }, error) {
			r, err := d.Protection.GetRecoveryPoint(ctx, in.ID)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RecoveryPointDTO }{rpDTO(r, true)}, nil
		})

	// ---- alerts ----
	huma.Register(a, op("list-alerts", http.MethodGet, "/api/v1/alerts", backupTag,
		"List alerts", "Backup failures, partial recovery points, quiesce warnings, agent auto-resumes and applications not resumed.", rbac.BackupRead),
		func(ctx context.Context, in *struct {
			All   bool  `query:"all" doc:"Include acknowledged alerts."`
			Limit int32 `query:"limit" minimum:"1" maximum:"500" default:"100"`
		}) (*struct {
			Body struct {
				Items []AlertDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Protection.ListAlerts(ctx, !in.All, in.Limit)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []AlertDTO `json:"items"`
				}
			}{}
			out.Body.Items = []AlertDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, AlertDTO{ID: r.ID, Severity: r.Severity, Type: r.EventType, TargetType: r.TargetType,
					TargetID: r.TargetID, Message: r.Message, Details: r.Payload, CreatedAt: r.CreatedAt, AcknowledgedAt: r.AcknowledgedAt})
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("acknowledge-alert", http.MethodPost, "/api/v1/alerts/{id}/acknowledge", backupTag,
		"Acknowledge an alert", "", rbac.BackupExecute, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct{}, error) {
			return nil, d.protErr(ctx, d.Protection.AcknowledgeAlert(ctx, principal(ctx), in.ID, metaFrom(ctx)))
		})
}
