// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/fsmeta"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

const (
	progressInterval = 5 * time.Second
	kindSeed         = "seed"
	captureLive      = "live"
)

var kindNames = map[agentv1.ComponentKind]string{
	agentv1.ComponentKind_COMPONENT_KIND_CONFIG:     manifest.KindConfig,
	agentv1.ComponentKind_COMPONENT_KIND_VOLUME:     manifest.KindVolume,
	agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT: manifest.KindBindMount,
	agentv1.ComponentKind_COMPONENT_KIND_DATABASE:   manifest.KindDatabase,
	agentv1.ComponentKind_COMPONENT_KIND_IMAGE:      manifest.KindImage,
	agentv1.ComponentKind_COMPONENT_KIND_FSMETA:     manifest.KindFSMeta,
}

// snapshotJob is one SnapshotComponents execution.
type snapshotJob struct {
	a     *Agent
	cmdID string
	c     *agentv1.SnapshotComponentsCommand
	repo  engine.Repository
	user  string
	host  string
	total int

	mu       sync.Mutex
	lastEmit time.Time
	done     int
}

// snapshotComponents captures every component into the configured
// Repository. It succeeds with per-component results; it fails only for
// infrastructure problems or cancellation (nothing partial is saved).
func (a *Agent) snapshotComponents(ctx context.Context, cmdID string, c *agentv1.SnapshotComponentsCommand) (*agentv1.SnapshotComponentsResult, error) {
	if c.RecoveryPointId == "" || c.ApplicationId == "" {
		return nil, permanent(errors.New("recovery_point_id and application_id are required"))
	}
	if _, err := a.loadConnection(c.RepositoryId); err != nil {
		return nil, err
	}
	if !a.jobs.tryAcquire() {
		a.progress(cmdID, map[string]any{"queued": true})
		if err := a.jobs.acquire(ctx); err != nil {
			return nil, err
		}
	}
	defer a.jobs.release()
	h, err := a.openRepo(ctx, c.RepositoryId)
	if err != nil {
		return nil, err
	}
	defer h.release()

	j := &snapshotJob{a: a, cmdID: cmdID, c: c, repo: h.repo, user: h.conn.Username, host: h.conn.Hostname}
	for _, s := range c.Components {
		j.total++
		if s.CaptureFsmeta && (s.Kind == agentv1.ComponentKind_COMPONENT_KIND_VOLUME || s.Kind == agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT) {
			j.total++
		}
	}
	res := &agentv1.SnapshotComponentsResult{}
	var stopReason string
	add := func(r *agentv1.ComponentResult) {
		res.Components = append(res.Components, r)
		j.mu.Lock()
		j.done++
		j.mu.Unlock()
		if r.Required && r.Status == manifest.ComponentFailed && stopReason == "" {
			stopReason = fmt.Sprintf("skipped: required component %s failed", r.Name)
		}
	}
	for _, s := range c.Components {
		if stopReason != "" {
			add(j.skipped(s, s.Name, stopReason))
			if j.hasFSMeta(s) {
				add(j.skippedFSMeta(s, stopReason))
			}
			continue
		}
		r, err := j.component(ctx, s)
		if err != nil {
			return nil, err // cancelled
		}
		add(r)
		if !j.hasFSMeta(s) {
			continue
		}
		if r.Status != manifest.ComponentSucceeded {
			reason := stopReason
			if reason == "" {
				reason = "skipped: parent component " + s.Name + " did not succeed"
			}
			add(j.skippedFSMeta(s, reason))
			continue
		}
		fr, err := j.fsmeta(ctx, s)
		if err != nil {
			return nil, err
		}
		add(fr)
	}
	j.emit(true, "", engine.Progress{})
	return res, nil
}

func (j *snapshotJob) hasFSMeta(s *agentv1.ComponentSpec) bool {
	return s.CaptureFsmeta && (s.Kind == agentv1.ComponentKind_COMPONENT_KIND_VOLUME || s.Kind == agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT)
}

func fsmetaName(parent string) string { return manifest.KindFSMeta + ":" + parent }

func (j *snapshotJob) base(s *agentv1.ComponentSpec, name string, kind agentv1.ComponentKind) *agentv1.ComponentResult {
	return &agentv1.ComponentResult{Name: name, Kind: kind, Required: s.Required, Path: s.Path, VolumeName: s.VolumeName,
		CaptureMethod: captureLive}
}

func (j *snapshotJob) skipped(s *agentv1.ComponentSpec, name, reason string) *agentv1.ComponentResult {
	r := j.base(s, name, s.Kind)
	r.Status, r.Error = manifest.ComponentSkipped, reason
	return r
}

func (j *snapshotJob) skippedFSMeta(s *agentv1.ComponentSpec, reason string) *agentv1.ComponentResult {
	r := j.skipped(s, fsmetaName(s.Name), reason)
	r.Kind, r.Parent = agentv1.ComponentKind_COMPONENT_KIND_FSMETA, s.Name
	return r
}

func (j *snapshotJob) request(name, kind string) engine.SnapshotRequest {
	if j.c.Seed {
		kind = kindSeed
	}
	return engine.SnapshotRequest{
		Source: engine.Source{User: j.user, Host: j.host, Path: "/" + j.c.ApplicationId + "/" + name},
		Tags: map[string]string{engine.TagRP: j.c.RecoveryPointId, engine.TagApp: j.c.ApplicationId,
			engine.TagComponent: name, engine.TagKind: kind},
		Pins:             []string{engine.Pin},
		Description:      "dbr2 " + j.c.RecoveryPointId + " " + name,
		Incremental:      true,
		ProgressInterval: progressInterval,
		OnProgress:       func(p engine.Progress) { j.emit(false, name, p) },
	}
}

// emit reports progress at most every progressInterval (force = final).
func (j *snapshotJob) emit(force bool, name string, p engine.Progress) {
	j.mu.Lock()
	if !force && time.Since(j.lastEmit) < progressInterval {
		j.mu.Unlock()
		return
	}
	j.lastEmit = time.Now()
	done := j.done
	j.mu.Unlock()
	doc := map[string]any{"done": done, "total": j.total}
	if name != "" {
		doc["component"], doc["hashed_bytes"], doc["uploaded_bytes"], doc["files"] = name, p.HashedBytes, p.UploadedBytes, p.Files
	}
	j.a.progress(j.cmdID, doc)
}

// progress sends a RUNNING update with a JSON progress document.
func (a *Agent) progress(cmdID string, doc map[string]any) {
	b, _ := json.Marshal(doc)
	u := update(cmdID, agentv1.CommandState_COMMAND_STATE_RUNNING)
	u.Progress = b
	a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_CommandUpdate{CommandUpdate: u}})
}

// component captures one spec. The error is non-nil only on cancellation;
// component failures are reported in the result.
func (j *snapshotJob) component(ctx context.Context, s *agentv1.ComponentSpec) (*agentv1.ComponentResult, error) {
	r := j.base(s, s.Name, s.Kind)
	r.StartedUnixMs = time.Now().UnixMilli()
	kind, ok := kindNames[s.Kind]
	var snap *engine.Snapshot
	var err error
	switch {
	case s.Name == "":
		err = errors.New("component has no name")
	case !ok:
		err = fmt.Errorf("unsupported component kind %s", s.Kind)
	case kind == manifest.KindDatabase || kind == manifest.KindImage:
		r.Status, r.Error = manifest.ComponentSkipped, "not supported until Phase 8"
		r.FinishedUnixMs = time.Now().UnixMilli()
		return r, nil
	case kind == manifest.KindFSMeta:
		err = errors.New("fsmeta components are produced by capture_fsmeta on their parent")
	case kind == manifest.KindConfig:
		snap, err = j.config(ctx, s, r)
	default:
		snap, err = j.filesystem(ctx, s, r, kind)
	}
	if ctx.Err() != nil || errors.Is(err, engine.ErrCanceled) {
		return nil, fmt.Errorf("snapshot of %s cancelled: %w", s.Name, errors.Join(ctx.Err(), err))
	}
	j.finish(r, snap, err)
	return r, nil
}

func (j *snapshotJob) finish(r *agentv1.ComponentResult, snap *engine.Snapshot, err error) {
	r.FinishedUnixMs = time.Now().UnixMilli()
	if snap != nil {
		r.SnapshotId, r.RootObjectId, r.Source = snap.ID, snap.RootObjectID, snap.Source.String()
		r.SizeBytes, r.Files = snap.TotalBytes, snap.Files
		if snap.Errors > 0 && err == nil {
			err = fmt.Errorf("%d entries could not be read", snap.Errors)
		}
	}
	if err != nil {
		r.Status = manifest.ComponentFailed
		r.Error = joinNote(strings.ToValidUTF8(err.Error(), "�"), r.Error)
		j.a.log.Warn("component capture failed", "recovery_point_id", j.c.RecoveryPointId, "component", r.Name, "err", r.Error)
		return
	}
	r.Status = manifest.ComponentSucceeded
	j.a.log.Info("component captured", "recovery_point_id", j.c.RecoveryPointId, "component", r.Name,
		"snapshot_id", r.SnapshotId, "bytes", r.SizeBytes, "files", r.Files)
}

func joinNote(a, b string) string {
	if b == "" {
		return a
	}
	return a + "; " + b
}

// filesystem snapshots a volume or bind-mount path and records its root
// ownership, mode and SELinux context (ADR-0006). A bind mount of a single
// regular file is stored as a stream snapshot named after the file.
func (j *snapshotJob) filesystem(ctx context.Context, s *agentv1.ComponentSpec, r *agentv1.ComponentResult, kind string) (*engine.Snapshot, error) {
	if s.Path == "" || !filepath.IsAbs(s.Path) {
		return nil, fmt.Errorf("component path %q is not absolute", s.Path)
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		return nil, err
	}
	isFile := fi.Mode().IsRegular() && kind == manifest.KindBindMount
	if !fi.IsDir() && !isFile {
		return nil, fmt.Errorf("%s is %s; only directories (and regular files for bind mounts) can be captured",
			s.Path, fileType(fi.Mode()))
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		r.OwnerUid, r.OwnerGid = st.Uid, st.Gid
		r.Mode = fmt.Sprintf("%04o", st.Mode&0o7777)
	}
	r.SelinuxContext = fsmeta.SELinuxContext(s.Path)
	if !isFile {
		return j.repo.SnapshotPath(ctx, s.Path, j.request(s.Name, kind))
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return j.repo.SnapshotStream(ctx, filepath.Base(s.Path), f, j.request(s.Name, kind))
}

func fileType(m os.FileMode) string {
	switch {
	case m&os.ModeSocket != 0:
		return "a socket"
	case m&os.ModeNamedPipe != 0:
		return "a named pipe"
	case m&os.ModeDevice != 0:
		return "a device"
	case m.IsRegular():
		return "a regular file"
	}
	return "not a directory"
}

// fsmeta streams the parent's filesystem metadata record into its own
// snapshot.
func (j *snapshotJob) fsmeta(ctx context.Context, s *agentv1.ComponentSpec) (*agentv1.ComponentResult, error) {
	name := fsmetaName(s.Name)
	r := j.base(s, name, agentv1.ComponentKind_COMPONENT_KIND_FSMETA)
	r.Parent, r.VolumeName = s.Name, s.VolumeName
	r.StartedUnixMs = time.Now().UnixMilli()
	pr, pw := io.Pipe()
	var st fsmeta.Stats
	werr := make(chan error, 1)
	go func() {
		var err error
		st, err = fsmeta.Write(ctx, pw, s.Path)
		pw.CloseWithError(err)
		werr <- err
	}()
	snap, err := j.repo.SnapshotStream(ctx, fsmeta.FileName, pr, j.request(name, manifest.KindFSMeta))
	pr.CloseWithError(errors.New("snapshot finished"))
	if e := <-werr; e != nil && err == nil {
		err = fmt.Errorf("fsmeta walk: %w", e)
		snap = nil // an upload of a broken stream must not count
	}
	if ctx.Err() != nil || errors.Is(err, engine.ErrCanceled) {
		return nil, fmt.Errorf("snapshot of %s cancelled: %w", name, errors.Join(ctx.Err(), err))
	}
	if err == nil && st.Errors > 0 {
		r.Error = fmt.Sprintf("%d paths had unreadable metadata (recorded in the fsmeta record)", st.Errors)
	}
	j.finish(r, snap, err)
	return r, nil
}

// config stages Compose/env files, container inspect documents and the
// discovery metadata in a private temp dir and snapshots it.
func (j *snapshotJob) config(ctx context.Context, s *agentv1.ComponentSpec, r *agentv1.ComponentResult) (*engine.Snapshot, error) {
	base := j.a.cfg.path(tmpDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(base, "config-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	var missing []string
	staged := 0
	for _, f := range s.Files {
		if err := stageFile(stage, f); err != nil {
			missing = append(missing, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		staged++
	}
	if len(s.MetadataJson) > 0 {
		if err := os.WriteFile(filepath.Join(stage, "discovery.json"), s.MetadataJson, 0o600); err != nil {
			return nil, err
		}
	}
	inspected, err := j.stageContainers(ctx, stage, s.ContainerIds)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		r.Error = "missing files: " + strings.Join(missing, "; ")
	}
	if staged == 0 && len(s.MetadataJson) == 0 && inspected == 0 {
		if len(s.Files) == 0 {
			return nil, errors.New("nothing to capture (no files, metadata or containers)")
		}
		return nil, errors.New("none of the configuration files could be read and no metadata was given")
	}
	return j.repo.SnapshotPath(ctx, stage, j.request(s.Name, manifest.KindConfig))
}

// stageFile copies an absolute host file to <stage>/files/<path>, keeping
// its mode and mtime.
func stageFile(stage, src string) error {
	if !filepath.IsAbs(src) {
		return errors.New("not an absolute path")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	dst := filepath.Join(stage, "files", filepath.Clean(src))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, fi.Mode().Perm()); err != nil { // umask-proof
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

// stageContainers writes each container's raw inspect document (unredacted:
// it is the restore source for env) to containers/<id>.json; failures go to
// containers/errors.json. It returns the number staged.
func (j *snapshotJob) stageContainers(ctx context.Context, stage string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	dir := filepath.Join(stage, "containers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	errs := map[string]string{}
	ctl, cerr := j.a.control()
	n := 0
	for _, id := range ids {
		if strings.ContainsAny(id, `/\`) || id == "" || id == "." || id == ".." {
			errs[id] = "invalid container id"
			continue
		}
		if cerr != nil {
			errs[id] = cerr.Error()
			continue
		}
		raw, err := ctl.InspectRaw(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			errs[id] = err.Error()
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o600); err != nil {
			return 0, err
		}
		n++
	}
	if len(errs) > 0 {
		b, _ := json.MarshalIndent(errs, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "errors.json"), b, 0o600); err != nil {
			return 0, err
		}
	}
	return n, nil
}
