// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
)

// AgentDTO is a host's agent.
type AgentDTO struct {
	ID              string     `json:"id" format:"uuid"`
	Hostname        string     `json:"hostname"`
	Status          string     `json:"status" enum:"pending,active,suspended,revoked"`
	StatusReason    *string    `json:"status_reason"`
	Connected       bool       `json:"connected" doc:"The agent has a live gateway session."`
	Outdated        bool       `json:"outdated" doc:"Older than the agent version this server ships."`
	AgentVersion    string     `json:"agent_version"`
	ProtocolVersion string     `json:"protocol_version"`
	OSRelease       *string    `json:"os_release"`
	Architecture    *string    `json:"architecture"`
	EnrolledAt      time.Time  `json:"enrolled_at"`
	ApprovedAt      *time.Time `json:"approved_at"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	LatencyMs       *int32     `json:"latency_ms"`
	DockerReachable *bool      `json:"docker_reachable"`
	DockerVersion   *string    `json:"docker_version"`
	HealthError     *string    `json:"health_error"`
	CertNotAfter    *time.Time `json:"certificate_not_after"`
}

func agentDTO(a fleet.Agent) AgentDTO {
	return AgentDTO{ID: a.ID.String(), Hostname: a.Hostname, Status: a.Status, StatusReason: a.StatusReason, Connected: a.Connected,
		Outdated: a.Outdated, AgentVersion: a.AgentVersion, ProtocolVersion: a.ProtocolVersion, OSRelease: a.OsRelease,
		Architecture: a.Architecture, EnrolledAt: a.EnrolledAt, ApprovedAt: a.ApprovedAt, LastSeenAt: a.LastSeenAt,
		LatencyMs: a.LastLatencyMs, DockerReachable: a.DockerReachable, DockerVersion: a.DockerVersion, HealthError: a.HealthError,
		CertNotAfter: a.CertNotAfter}
}

// RegistrationTokenDTO describes an enrollment token (never its secret).
type RegistrationTokenDTO struct {
	ID          string     `json:"id" format:"uuid"`
	Prefix      string     `json:"prefix"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at"`
	UsedByAgent *string    `json:"used_by_agent"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

// ApplicationSummary is an application in the list.
type ApplicationSummary struct {
	ID              string     `json:"id" format:"uuid"`
	Name            string     `json:"name"`
	DisplayName     *string    `json:"display_name"`
	Kind            string     `json:"kind" enum:"compose,container,manual"`
	HostID          string     `json:"host_id" format:"uuid"`
	Hostname        string     `json:"hostname"`
	Source          string     `json:"source" enum:"original,reconstructed,unknown" doc:"Provenance of the Compose definition."`
	Services        int        `json:"services"`
	Containers      int        `json:"containers"`
	Volumes         int        `json:"volumes"`
	BindMounts      int        `json:"bind_mounts"`
	UnprotectedHigh int        `json:"unprotected_high" doc:"Writable paths not backed by a volume or bind mount (high severity)."`
	Dependencies    int        `json:"dependencies"`
	SecretsCount    int        `json:"secrets_count"`
	Owner           *string    `json:"owner"`
	Environment     *string    `json:"environment"`
	Criticality     *string    `json:"criticality"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	MissingSince    *time.Time `json:"missing_since" doc:"Set when the application disappeared from the host's latest inventory."`
}

func summary(a fleet.Application) ApplicationSummary {
	r := a.Record
	s := ApplicationSummary{ID: r.ID.String(), Name: r.Name, DisplayName: r.DisplayName, Kind: r.Kind, HostID: r.AgentID.String(),
		Hostname: r.Hostname, Source: "unknown", Owner: r.Owner, Environment: r.Environment, Criticality: r.Criticality,
		LastSeenAt: r.LastSeenAt, MissingSince: r.MissingSince}
	if x := a.Analysis; x != nil {
		s.Source, s.Services, s.Containers, s.Volumes, s.BindMounts = x.Source, len(x.Services), len(x.Containers), len(x.Volumes), len(x.BindMounts)
		s.Dependencies, s.SecretsCount = len(x.Dependencies), x.SecretsCount
		for _, u := range x.Unprotected {
			if u.Severity == "high" {
				s.UnprotectedHigh++
			}
		}
	}
	return s
}

// ContainerDTO is a container of an application (secrets masked).
type ContainerDTO struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Image    string             `json:"image"`
	State    string             `json:"state"`
	Env      []inventory.EnvVar `json:"env"`
	Ports    []inventory.Port   `json:"ports"`
	Mounts   []inventory.Mount  `json:"mounts"`
	Networks []string           `json:"networks"`
}

// ApplicationDetail is the full application view.
type ApplicationDetail struct {
	ApplicationSummary
	ManualContainers []string               `json:"manual_containers,omitempty"`
	Analysis         *inventory.Application `json:"analysis" doc:"Services, volumes (with classification), bind mounts, networks, images (digest and platform), dependencies and unprotected paths. Null when the application is missing from the latest inventory."`
	ContainersDetail []ContainerDTO         `json:"containers_detail"`
	CollectedAt      *time.Time             `json:"collected_at"`
}

// ComposeDTO is an application's Compose definition.
type ComposeDTO struct {
	Source        string            `json:"source" enum:"original,reconstructed"`
	Reason        string            `json:"reason,omitempty"`
	ConfigFiles   []FileDTO         `json:"config_files"`
	EnvFiles      []FileDTO         `json:"env_files"`
	Reconstructed string            `json:"reconstructed,omitempty" doc:"Generated Compose YAML (flagged RECONSTRUCTED; secrets as ${VAR} placeholders)."`
	Revealed      bool              `json:"revealed"`
	Secrets       map[string]string `json:"secrets,omitempty" doc:"Placeholder values; present only when revealed."`
}

// FileDTO is a file collected from the host.
type FileDTO struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Masked  bool   `json:"masked" doc:"Sensitive values are replaced by ********."`
	Error   string `json:"error,omitempty"`
}

func files(fs []inventory.File, revealed bool) []FileDTO {
	out := make([]FileDTO, 0, len(fs))
	for _, f := range fs {
		out = append(out, FileDTO{Path: f.Path, Content: f.Content, Masked: f.Masked && !revealed, Error: f.Error})
	}
	return out
}

type idPath struct {
	ID string `path:"id" format:"uuid"`
}

type reasonInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Reason string `json:"reason" minLength:"1" maxLength:"500" doc:"Stored in the audit log."`
	}
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, huma.Error422UnprocessableEntity("invalid id")
	}
	return id, nil
}

func (d *Deps) fleetErr(ctx context.Context, err error) error {
	var busy *temporalx.ErrOperationInProgress
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fleet.ErrNotFound):
		return problem(http.StatusNotFound, CodeNotFound, "not found")
	case errors.Is(err, fleet.ErrInvalidTransition), errors.As(err, &busy):
		return problem(http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, fleet.ErrInvalid):
		return problem(http.StatusBadRequest, CodeValidation, err.Error())
	case errors.Is(err, fleet.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, err.Error())
	case errors.Is(err, fleet.ErrNoInventory):
		return problem(http.StatusConflict, CodeConflict, err.Error())
	}
	return d.fail(ctx, err)
}

func registerFleet(a huma.API, d *Deps) {
	// ---- agents ----
	huma.Register(a, op("list-agents", http.MethodGet, "/api/v1/agents", "Hosts",
		"List agents", "Lists every enrolled host agent with its status, versions, health and live connection state.", rbac.HostRead),
		func(ctx context.Context, _ *struct{}) (*struct{ Body struct{ Items []AgentDTO `json:"items"` } }, error) {
			rows, err := d.Fleet.ListAgents(ctx)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct{ Body struct{ Items []AgentDTO `json:"items"` } }{}
			out.Body.Items = make([]AgentDTO, 0, len(rows))
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, agentDTO(r))
			}
			return out, nil
		})

	huma.Register(a, op("get-agent", http.MethodGet, "/api/v1/agents/{id}", "Hosts",
		"Get an agent", "Returns one agent.", rbac.HostRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body AgentDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			ag, err := d.Fleet.GetAgent(ctx, id)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			return &struct{ Body AgentDTO }{Body: agentDTO(ag)}, nil
		})

	for _, act := range []struct{ action, summary, desc string }{
		{fleet.ActionApprove, "Approve an agent", "Host approval workflow: a newly enrolled agent stays pending (its sessions are refused) until approved."},
		{fleet.ActionSuspend, "Suspend an agent", "Ends the agent's session and refuses new ones until resumed. No data is deleted."},
		{fleet.ActionResume, "Resume an agent", "Allows a suspended agent to connect again."},
		{fleet.ActionRevoke, "Revoke an agent", "Permanently revokes the agent and all of its certificates. The host must enroll again with a new token."},
	} {
		action := act.action
		huma.Register(a, op(action+"-agent", http.MethodPost, "/api/v1/agents/{id}/"+action, "Hosts", act.summary, act.desc,
			rbac.HostManage, http.StatusNotFound, http.StatusConflict),
			func(ctx context.Context, in *reasonInput) (*struct{ Body AgentDTO }, error) {
				id, err := parseID(in.ID)
				if err != nil {
					return nil, err
				}
				ag, err := d.Fleet.SetStatus(ctx, principal(ctx), id, action, in.Body.Reason, metaFrom(ctx))
				if err != nil {
					return nil, d.fleetErr(ctx, err)
				}
				return &struct{ Body AgentDTO }{Body: agentDTO(ag)}, nil
			})
	}

	huma.Register(a, withStatus(op("discover-agent", http.MethodPost, "/api/v1/agents/{id}/discover", "Hosts",
		"Run discovery now", "Starts the DiscoverHost workflow; the agent's inventory and applications update when it completes. "+
			"Agents also report their inventory periodically.", rbac.HostManage, http.StatusNotFound, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, in *idPath) (*struct{ Body struct{ WorkflowID string `json:"workflow_id"` } }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			wf, err := d.Fleet.RequestDiscovery(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct{ Body struct{ WorkflowID string `json:"workflow_id"` } }{}
			out.Body.WorkflowID = wf
			return out, nil
		})

	huma.Register(a, op("get-agent-inventory", http.MethodGet, "/api/v1/agents/{id}/inventory", "Hosts",
		"Get a host's inventory", "Returns the latest discovery inventory of the host (containers, volumes, networks, images, "+
			"Compose files). Sensitive values are masked.", rbac.HostRead, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *idPath) (*struct {
			Body struct {
				ReceivedAt time.Time            `json:"received_at"`
				Inventory  *inventory.Inventory `json:"inventory"`
			}
		}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			inv, at, err := d.Fleet.Inventory(ctx, id)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct {
				Body struct {
					ReceivedAt time.Time            `json:"received_at"`
					Inventory  *inventory.Inventory `json:"inventory"`
				}
			}{}
			out.Body.ReceivedAt, out.Body.Inventory = at, inv
			return out, nil
		})

	// ---- registration tokens ----
	huma.Register(a, op("list-registration-tokens", http.MethodGet, "/api/v1/agents/registration-tokens", "Hosts",
		"List registration tokens", "Lists agent registration tokens (secrets are never returned).", rbac.HostManage),
		func(ctx context.Context, _ *struct{}) (*struct{ Body struct{ Items []RegistrationTokenDTO `json:"items"` } }, error) {
			rows, err := d.Fleet.ListRegistrationTokens(ctx)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct{ Body struct{ Items []RegistrationTokenDTO `json:"items"` } }{}
			out.Body.Items = make([]RegistrationTokenDTO, 0, len(rows))
			for _, r := range rows {
				t := RegistrationTokenDTO{ID: r.ID.String(), Prefix: r.Prefix, Description: r.Description, CreatedAt: r.CreatedAt,
					ExpiresAt: r.ExpiresAt, UsedAt: r.UsedAt, RevokedAt: r.RevokedAt}
				if r.UsedByAgent != nil {
					s := r.UsedByAgent.String()
					t.UsedByAgent = &s
				}
				out.Body.Items = append(out.Body.Items, t)
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("create-registration-token", http.MethodPost, "/api/v1/agents/registration-tokens", "Hosts",
		"Create a registration token", "Creates a single-use, expiring token for enrolling one host, and returns the join command "+
			"(including the pinned CA fingerprint). The token is shown only once.", rbac.HostManage, http.StatusBadRequest), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Description    string `json:"description" minLength:"1" maxLength:"200"`
				ExpiresInHours int    `json:"expires_in_hours,omitempty" minimum:"1" maximum:"168" default:"24"`
			}
		}) (*struct {
			Body struct {
				Token          string               `json:"token" doc:"Shown only once."`
				GatewayAddress string               `json:"gateway_address"`
				CASHA256       string               `json:"ca_sha256" doc:"SHA-256 fingerprint of the DBR² agent CA, pinned by the agent at enrollment."`
				JoinCommand    string               `json:"join_command"`
				Details        RegistrationTokenDTO `json:"registration_token"`
			}
		}, error) {
			t, err := d.Fleet.CreateRegistrationToken(ctx, principal(ctx), in.Body.Description, time.Duration(in.Body.ExpiresInHours)*time.Hour, metaFrom(ctx))
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Token          string               `json:"token" doc:"Shown only once."`
					GatewayAddress string               `json:"gateway_address"`
					CASHA256       string               `json:"ca_sha256" doc:"SHA-256 fingerprint of the DBR² agent CA, pinned by the agent at enrollment."`
					JoinCommand    string               `json:"join_command"`
					Details        RegistrationTokenDTO `json:"registration_token"`
				}
			}{}
			out.Body.Token, out.Body.GatewayAddress, out.Body.CASHA256, out.Body.JoinCommand = t.Token, t.GatewayAddress, t.CASHA256, t.JoinCommand
			out.Body.Details = RegistrationTokenDTO{ID: t.ID.String(), Prefix: t.Prefix, Description: t.Description, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
			return out, nil
		})

	huma.Register(a, withStatus(op("revoke-registration-token", http.MethodDelete, "/api/v1/agents/registration-tokens/{id}", "Hosts",
		"Revoke a registration token", "Revokes an unused registration token.", rbac.HostManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, d.fleetErr(ctx, d.Fleet.RevokeRegistrationToken(ctx, principal(ctx), id, metaFrom(ctx)))
		})

	// ---- applications ----
	huma.Register(a, op("list-applications", http.MethodGet, "/api/v1/applications", "Applications",
		"List applications", "Lists every discovered or manually defined application with protection-relevant counts.", rbac.ApplicationRead),
		func(ctx context.Context, _ *struct{}) (*struct{ Body struct{ Items []ApplicationSummary `json:"items"` } }, error) {
			apps, err := d.Fleet.ListApplications(ctx)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct{ Body struct{ Items []ApplicationSummary `json:"items"` } }{}
			out.Body.Items = make([]ApplicationSummary, 0, len(apps))
			for _, x := range apps {
				out.Body.Items = append(out.Body.Items, summary(x))
			}
			return out, nil
		})

	huma.Register(a, op("get-application", http.MethodGet, "/api/v1/applications/{id}", "Applications",
		"Get an application", "Returns the application's services, containers (secrets masked), volumes with classification, bind mounts, "+
			"networks, images with digests, external dependencies and unprotected paths.", rbac.ApplicationRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body ApplicationDetail }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			app, err := d.Fleet.GetApplication(ctx, id)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			det := ApplicationDetail{ApplicationSummary: summary(app), ManualContainers: app.Record.ManualContainers, Analysis: app.Analysis,
				ContainersDetail: []ContainerDTO{}}
			if inv := app.Inventory; inv != nil && app.Analysis != nil {
				inventory.Redact(inv)
				t := inv.CollectedAt
				det.CollectedAt = &t
				in := map[string]bool{}
				for _, c := range app.Analysis.Containers {
					in[c] = true
				}
				for _, c := range inv.Containers {
					if in[c.Name] {
						det.ContainersDetail = append(det.ContainersDetail, ContainerDTO{ID: c.ID, Name: c.Name, Image: c.Image, State: c.State,
							Env: nonNilEnv(c.Env), Ports: nonNilPorts(c.Ports), Mounts: nonNilMounts(c.Mounts), Networks: nonNilStr(c.Networks)})
					}
				}
			}
			return &struct{ Body ApplicationDetail }{Body: det}, nil
		})

	huma.Register(a, op("update-application", http.MethodPatch, "/api/v1/applications/{id}", "Applications",
		"Update application metadata", "Sets ownership metadata: display name, owner, environment and criticality. "+
			"Empty strings clear a field; omitted fields are unchanged.", rbac.ApplicationManage, http.StatusNotFound, http.StatusBadRequest),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body struct {
				DisplayName *string `json:"display_name,omitempty" maxLength:"200"`
				Owner       *string `json:"owner,omitempty" maxLength:"200"`
				Environment *string `json:"environment,omitempty" doc:"production, staging, development, test or other"`
				Criticality *string `json:"criticality,omitempty" doc:"critical, high, medium or low"`
			}
		}) (*struct{ Body ApplicationSummary }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			cur, err := d.Fleet.GetApplication(ctx, id)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			md := fleet.Metadata{DisplayName: keep(in.Body.DisplayName, cur.Record.DisplayName), Owner: keep(in.Body.Owner, cur.Record.Owner),
				Environment: keep(in.Body.Environment, cur.Record.Environment), Criticality: keep(in.Body.Criticality, cur.Record.Criticality)}
			if err := d.Fleet.UpdateMetadata(ctx, principal(ctx), id, md, metaFrom(ctx)); err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			app, err := d.Fleet.GetApplication(ctx, id)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			return &struct{ Body ApplicationSummary }{Body: summary(app)}, nil
		})

	huma.Register(a, withStatus(op("create-application", http.MethodPost, "/api/v1/applications", "Applications",
		"Create a manual application", "Groups standalone (non-Compose) containers of one host into an application.",
		rbac.ApplicationManage, http.StatusBadRequest, http.StatusConflict), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name       string   `json:"name" minLength:"1" maxLength:"200"`
				HostID     string   `json:"host_id" format:"uuid"`
				Containers []string `json:"containers" minItems:"1"`
			}
		}) (*struct{ Body struct{ ID string `json:"id"` } }, error) {
			host, err := parseID(in.Body.HostID)
			if err != nil {
				return nil, err
			}
			id, err := d.Fleet.CreateManual(ctx, principal(ctx), host, in.Body.Name, in.Body.Containers, metaFrom(ctx))
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			out := &struct{ Body struct{ ID string `json:"id"` } }{}
			out.Body.ID = id.String()
			return out, nil
		})

	huma.Register(a, withStatus(op("delete-application", http.MethodDelete, "/api/v1/applications/{id}", "Applications",
		"Delete a manual application", "Deletes a manual application; its containers become standalone applications again.",
		rbac.ApplicationManage, http.StatusNotFound, http.StatusBadRequest), http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, d.fleetErr(ctx, d.Fleet.DeleteManual(ctx, principal(ctx), id, metaFrom(ctx)))
		})

	huma.Register(a, op("get-application-compose", http.MethodGet, "/api/v1/applications/{id}/compose", "Applications",
		"Get the Compose definition", "Returns the original Compose and .env files (sensitive values masked), or a RECONSTRUCTED definition "+
			"generated from runtime metadata. `reveal=true` shows secrets; it requires `secrets.read` and is audited.",
		rbac.ApplicationRead, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID     string `path:"id" format:"uuid"`
			Reveal bool   `query:"reveal" doc:"Reveal secret values (requires secrets.read; audited)."`
		}) (*struct{ Body ComposeDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := d.Fleet.Compose(ctx, principal(ctx), id, in.Reveal, metaFrom(ctx))
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			return &struct{ Body ComposeDTO }{Body: ComposeDTO{Source: c.Source, Reason: c.Reason, ConfigFiles: files(c.ConfigFiles, c.Revealed),
				EnvFiles: files(c.EnvFiles, c.Revealed), Reconstructed: c.Reconstructed, Revealed: c.Revealed, Secrets: c.Secrets}}, nil
		})
}

// keep returns v when provided, else the current value.
func keep(v, cur *string) *string {
	if v != nil {
		return v
	}
	return cur
}

func nonNilEnv(v []inventory.EnvVar) []inventory.EnvVar {
	if v == nil {
		return []inventory.EnvVar{}
	}
	return v
}
func nonNilPorts(v []inventory.Port) []inventory.Port {
	if v == nil {
		return []inventory.Port{}
	}
	return v
}
func nonNilMounts(v []inventory.Mount) []inventory.Mount {
	if v == nil {
		return []inventory.Mount{}
	}
	return v
}
func nonNilStr(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
