// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// StartBackup starts a manual backup (workflow ID application/<id>: one
// operation per application at a time; ADR-0011).
func (s *Service) StartBackup(ctx context.Context, p *auth.Principal, appID uuid.UUID, mode string, m auth.RequestMeta) (string, error) {
	app, err := s.fleet.GetApplication(ctx, appID)
	if err != nil {
		return "", err
	}
	if mode != "" && mode != manifest.ModeLive && mode != manifest.ModeQuiesced && mode != manifest.ModeOffline {
		return "", fmt.Errorf("%w: consistency_mode must be live, quiesced or offline", ErrInvalid)
	}
	if app.Analysis == nil {
		return "", fmt.Errorf("%w: the application is missing from its host's latest inventory", ErrConflict)
	}
	if err := s.readyToBackUp(ctx, app); err != nil {
		return "", err
	}
	if s.temporal == nil {
		return "", errors.New("workflow engine unavailable")
	}
	run, err := temporalx.StartApplicationOperation(ctx, s.temporal, s.opts.TaskQueue, appID.String(), backup.BackupWorkflow,
		backup.Input{ApplicationID: appID.String(), Trigger: "manual", RequestedBy: p.UserID.String(), ConsistencyMode: mode})
	if err != nil {
		return "", err
	}
	ev := s.event(p, m, audit.BackupRequested)
	ev.TargetType, ev.TargetID = "application", appID.String()
	ev.Details = map[string]any{"workflow_id": run.GetID(), "run_id": run.GetRunID(), "consistency_mode": mode, "trigger": "manual"}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return "", err
	}
	return run.GetID(), nil
}

// readyToBackUp reports, before a workflow starts, what PrepareBackup
// would refuse (the workflow re-checks: this is only for fast feedback).
func (s *Service) readyToBackUp(ctx context.Context, app fleet.Application) error {
	agent, err := s.fleet.GetAgent(ctx, app.Record.AgentID)
	if err != nil {
		return err
	}
	if agent.Status != "active" {
		return fmt.Errorf("%w: host %s is %s", ErrConflict, agent.Hostname, agent.Status)
	}
	set, err := s.backupSettings(ctx, s.q, app.Record.ID)
	if err != nil {
		return err
	}
	pol := s.policyFor(ctx, s.q, app.Record.PolicyID)
	var repo store.Repository
	switch {
	case set.RepositoryID != nil:
		repo, err = s.q.GetRepositoryByID(ctx, *set.RepositoryID)
	case pol != nil && pol.RepositoryID != nil:
		repo, err = s.q.GetRepositoryByID(ctx, *pol.RepositoryID)
	default:
		repo, err = s.q.GetDefaultRepository(ctx, s.opts.OrgID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no Repository is assigned to the application and no default Repository exists", ErrConflict)
	}
	if err != nil {
		return err
	}
	if repo.Status != "ready" {
		return fmt.Errorf("%w: Repository %s is %s (confirm its key escrow first)", ErrConflict, repo.Name, strings.ReplaceAll(repo.Status, "_", " "))
	}
	return nil
}

// RecoveryPointFilter narrows ListRecoveryPoints.
type RecoveryPointFilter struct {
	ApplicationID *uuid.UUID
	State         *string
	Limit         int32
}

// ListRecoveryPoints lists indexed recovery points, newest first.
func (s *Service) ListRecoveryPoints(ctx context.Context, f RecoveryPointFilter) ([]store.RecoveryPoint, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	return s.q.ListRecoveryPoints(ctx, store.ListRecoveryPointsParams{OrgID: s.opts.OrgID, Limit: f.Limit, ApplicationID: f.ApplicationID, State: f.State})
}

// GetRecoveryPoint returns one recovery point.
func (s *Service) GetRecoveryPoint(ctx context.Context, id string) (store.RecoveryPoint, error) {
	rp, err := s.q.GetRecoveryPoint(ctx, store.GetRecoveryPointParams{ID: id, OrgID: s.opts.OrgID})
	return rp, notFound(err)
}

// ListAlerts lists alerts (open only by default).
func (s *Service) ListAlerts(ctx context.Context, openOnly bool, limit int32) ([]store.NotificationOutbox, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.q.ListNotifications(ctx, store.ListNotificationsParams{OrgID: s.opts.OrgID, OpenOnly: openOnly, Limit: limit})
}

// AcknowledgeAlert acknowledges an alert.
func (s *Service) AcknowledgeAlert(ctx context.Context, p *auth.Principal, id int64, m auth.RequestMeta) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.AcknowledgeNotification(ctx, store.AcknowledgeNotificationParams{ID: id, OrgID: s.opts.OrgID, AcknowledgedBy: &p.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.event(p, m, audit.AlertAcknowledged)
		ev.TargetType, ev.TargetID = "alert", fmt.Sprint(id)
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// ---- Capture plan --------------------------------------------------------------------

// Bind-mount sources never captured: runtime sockets, kernel and host
// plumbing (restored by the host, not by DBR²).
var skipBindPrefixes = []string{"/proc", "/sys", "/dev", "/run", "/var/run", "/etc/localtime", "/etc/timezone",
	"/etc/hosts", "/etc/hostname", "/etc/resolv.conf", "/etc/machine-id", "/tmp/.X11-unix"}

func skipBind(src string) bool {
	if strings.HasSuffix(src, ".sock") || src == "/" {
		return true
	}
	for _, p := range skipBindPrefixes {
		if src == p || strings.HasPrefix(src, p+"/") {
			return true
		}
	}
	return false
}

type plan struct {
	components   []*agentv1.ComponentSpec
	containerIDs []string
	pre, post    []*agentv1.Hook
	seed         manifestSeedJSON
}

type manifestSeedJSON = []byte

// buildPlan derives the components to capture from the application's
// analysis and its settings (ADR-0004 defaults: config, volumes and bind
// mounts required; best-effort components optional).
func buildPlan(app fleet.Application, agent fleet.Agent, set BackupSettings) (*plan, error) {
	x := app.Analysis
	if x == nil {
		return nil, errors.New("the application is missing from its host's latest inventory")
	}
	excluded := setOf(set.ExcludedComponents)
	optional := setOf(set.OptionalComponents)
	add := func(p *plan, c *agentv1.ComponentSpec) {
		if excluded[c.Name] {
			return
		}
		c.Required = c.Kind == agentv1.ComponentKind_COMPONENT_KIND_CONFIG || !optional[c.Name]
		p.components = append(p.components, c)
	}
	p := &plan{}
	ids := map[string]string{} // service, container name and ID → ID
	for _, svc := range x.Services {
		for _, c := range svc.Containers {
			p.containerIDs = append(p.containerIDs, c.ID)
			ids[c.ID], ids[c.Name], ids[strings.TrimPrefix(c.Name, "/")] = c.ID, c.ID, c.ID
			if _, ok := ids[svc.Name]; !ok {
				ids[svc.Name] = c.ID
			}
		}
	}
	if len(p.containerIDs) == 0 {
		return nil, errors.New("the application has no containers")
	}

	var inv *inventory.Inventory
	if app.Inventory != nil {
		b, _ := json.Marshal(app.Inventory) // deep copy; the metadata is redacted
		inv = &inventory.Inventory{}
		_ = json.Unmarshal(b, inv)
		inventory.Redact(inv)
	}
	meta := map[string]any{"schema": "dbr2.config-metadata/v1", "application": x}
	if inv != nil {
		want := setOf(p.containerIDs)
		var cs []inventory.Container
		for _, c := range inv.Containers {
			if want[c.ID] {
				c.Changes = nil
				cs = append(cs, c)
			}
		}
		meta["containers"], meta["host"], meta["collected_at"] = cs, inv.Host, inv.CollectedAt
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range append(append([]inventory.File{}, x.ConfigFiles...), x.EnvFiles...) {
		if f.Path != "" {
			files = append(files, f.Path)
		}
	}
	add(p, &agentv1.ComponentSpec{Name: "config", Kind: agentv1.ComponentKind_COMPONENT_KIND_CONFIG, Path: x.WorkingDir,
		Files: files, MetadataJson: metaJSON, ContainerIds: p.containerIDs})

	// Databases (Phase 8): online logical dumps, and per strategy the
	// database containers' own volumes are skipped (logical) or kept.
	dbs := detectDatabases(x, inv)
	dbMounts := map[string]bool{} // volume names / bind sources owned only by database containers
	if set.DatabaseStrategy != StrategyVolume {
		for _, d := range dbs {
			add(p, &agentv1.ComponentSpec{Name: "database:" + d.name, Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE,
				Database: &agentv1.DatabaseSpec{Engine: d.engine, Format: d.format, ContainerId: d.containerID, Service: d.service,
					User: d.user, IncludeAof: d.engine == EngineRedis}})
		}
	}
	if set.DatabaseStrategy == StrategyLogical {
		for k := range exclusiveMounts(dbs, inv, p.containerIDs) {
			dbMounts[k] = true
		}
	}
	for _, v := range x.Volumes {
		if !v.Protected || v.Mountpoint == "" || dbMounts["volume:"+v.Name] {
			continue
		}
		add(p, &agentv1.ComponentSpec{Name: "volume:" + v.Name, Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME,
			Path: v.Mountpoint, VolumeName: v.Name, CaptureFsmeta: true})
	}
	seen := map[string]bool{}
	for _, b := range x.BindMounts {
		if b.Source == "" || seen[b.Source] || skipBind(b.Source) || dbMounts["bind:"+b.Source] {
			continue
		}
		seen[b.Source] = true
		add(p, &agentv1.ComponentSpec{Name: "bind:" + b.Source, Kind: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT,
			Path: b.Source, CaptureFsmeta: true})
	}
	resolve := func(hs []HookSpec) ([]*agentv1.Hook, error) {
		var out []*agentv1.Hook
		for _, h := range hs {
			id, ok := ids[h.Container]
			if !ok {
				return nil, fmt.Errorf("hook container %q is not part of the application", h.Container)
			}
			out = append(out, &agentv1.Hook{ContainerId: id, Command: h.Command, TimeoutSeconds: h.TimeoutSeconds, Optional: h.Optional})
		}
		return out, nil
	}
	if p.pre, err = resolve(set.PreHooks); err != nil {
		return nil, err
	}
	if p.post, err = resolve(set.PostHooks); err != nil {
		return nil, err
	}

	seed := backup.ManifestSeed{
		Application: manifest.Application{ID: app.Record.ID.String(), Name: displayName(app.Record), ComposeProject: x.ComposeProject, WorkingDir: x.WorkingDir},
		Source: manifest.Source{HostID: agent.ID.String(), AgentID: agent.ID.String(), Hostname: agent.Hostname,
			OSRelease: deref(agent.OsRelease), Architecture: deref(agent.Architecture), AgentVersion: agent.AgentVersion},
	}
	if inv != nil {
		seed.Source.RuntimeVersion = inv.Host.Runtime + " " + inv.Host.EngineVersion
	}
	seed.Topology = buildTopology(x, inv)
	for _, im := range x.Images {
		d := ""
		if len(im.Digests) > 0 {
			d = im.Digests[0]
		}
		seed.Images = append(seed.Images, manifest.Image{Ref: im.Reference, Digest: d})
	}
	if p.seed, err = json.Marshal(seed); err != nil {
		return nil, err
	}
	return p, nil
}

// seedComponents returns filesystem components with no content in the last
// committed recovery point: in a quiesced or offline backup they get a live
// seed pass first so the consistent pass only uploads the delta (ADR-0005).
func seedComponents(mode string, comps []*agentv1.ComponentSpec, last *manifest.Manifest) []string {
	if mode == manifest.ModeLive {
		return nil
	}
	have := map[string]bool{}
	if last != nil {
		for _, c := range last.Components {
			if c.Status == manifest.ComponentSucceeded {
				have[c.Name] = true
			}
		}
	}
	var out []string
	for _, c := range comps {
		if (c.Kind == agentv1.ComponentKind_COMPONENT_KIND_VOLUME || c.Kind == agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT) && !have[c.Name] {
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) lastManifest(ctx context.Context, appID uuid.UUID) *manifest.Manifest {
	rp, err := s.q.LastCommittedRecoveryPoint(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || len(rp.Manifest) == 0 {
		return nil
	}
	m, err := manifest.Parse(rp.Manifest)
	if err != nil {
		return nil
	}
	return m
}

func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func displayName(r store.ListApplicationsRow) string {
	if r.DisplayName != nil && *r.DisplayName != "" {
		return *r.DisplayName
	}
	return r.Name
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Database engines and dump formats (ADR-0017).
const (
	EnginePostgres = "postgresql"
	EngineRedis    = "redis"
	FormatPGDump   = "pg_dumpall-sql-zstd"
	FormatRDB      = "rdb"
)

type detectedDB struct {
	name, engine, format, containerID, service, user string
}

// databaseEngine recognizes PostgreSQL and Redis-compatible images by
// repository name (postgres, postgis/postgis, timescale/timescaledb,
// bitnami/postgresql, redis, bitnami/redis, valkey/valkey, redis/redis-stack…).
func databaseEngine(image string) string {
	ref := strings.ToLower(image)
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	name := ref[strings.LastIndex(ref, "/")+1:]
	for _, tool := range []string{"exporter", "pgadmin", "pgbouncer", "pgpool", "commander", "insight", "backup", "operator", "sentinel-ui", "-ui"} {
		if strings.Contains(name, tool) {
			return "" // tooling around a database, not the database
		}
	}
	switch {
	case strings.Contains(name, "postgres") || strings.Contains(name, "postgis") || strings.Contains(name, "timescaledb"):
		return EnginePostgres
	case strings.HasPrefix(name, "redis") || strings.HasPrefix(name, "valkey") || strings.Contains(name, "keydb"):
		return EngineRedis
	}
	return ""
}

// detectDatabases finds the application's database containers.
func detectDatabases(x *inventory.Application, inv *inventory.Inventory) []detectedDB {
	byID := map[string]inventory.Container{}
	if inv != nil {
		for _, c := range inv.Containers {
			byID[c.ID] = c
		}
	}
	var out []detectedDB
	for _, svc := range x.Services {
		for i, ref := range svc.Containers {
			image := svc.Image
			c, ok := byID[ref.ID]
			if ok && c.Image != "" {
				image = c.Image
			}
			engine := databaseEngine(image)
			if engine == "" {
				continue
			}
			d := detectedDB{name: svc.Name, engine: engine, containerID: strings.TrimPrefix(ref.Name, "/"), service: svc.Name}
			if len(svc.Containers) > 1 {
				d.name = fmt.Sprintf("%s-%d", svc.Name, i+1)
			}
			if engine == EnginePostgres {
				d.format = FormatPGDump
				for _, e := range c.Env {
					if e.Key == "POSTGRES_USER" && !e.Sensitive && e.Value != "" {
						d.user = e.Value
					}
				}
			} else {
				d.format = FormatRDB
			}
			out = append(out, d)
		}
	}
	return out
}

// exclusiveMounts returns the volumes and bind sources mounted only by
// database containers ("volume:<name>" / "bind:<source>").
func exclusiveMounts(dbs []detectedDB, inv *inventory.Inventory, appContainers []string) map[string]bool {
	if inv == nil || len(dbs) == 0 {
		return nil
	}
	isDB := map[string]bool{}
	for _, d := range dbs {
		isDB[d.containerID] = true
	}
	inApp := map[string]bool{}
	for _, id := range appContainers {
		inApp[id] = true
	}
	owners := map[string]map[bool]bool{} // mount → {db?: true}
	for _, c := range inv.Containers {
		if !inApp[c.ID] {
			continue
		}
		db := isDB[strings.TrimPrefix(c.Name, "/")]
		for _, m := range c.Mounts {
			key := ""
			switch m.Type {
			case "volume":
				key = "volume:" + m.Name
			case "bind":
				key = "bind:" + m.Source
			default:
				continue
			}
			if owners[key] == nil {
				owners[key] = map[bool]bool{}
			}
			owners[key][db] = true
		}
	}
	out := map[string]bool{}
	for k, o := range owners {
		if o[true] && !o[false] {
			out[k] = true
		}
	}
	return out
}
