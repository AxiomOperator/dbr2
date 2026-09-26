// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/fsmeta"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// Restore component statuses.
const (
	restored      = "restored"
	statusFailed  = "failed"
	statusSkipped = "skipped"
)

var errLeaseExpired = permanent(errors.New("the restore's stop lease expired and the application was restarted; nothing was swapped"))

// restoreControl returns the runtime's restore capability.
func (a *Agent) restoreControl() (runtime.RestoreControl, error) {
	if a.rt == nil {
		return nil, errors.New("container runtime unavailable")
	}
	c, ok := a.rt.(runtime.RestoreControl)
	if !ok {
		return nil, permanent(errors.New("container runtime " + a.rt.Name() + " cannot restore applications"))
	}
	return c, nil
}

func validRestoreID(id string) error {
	if !repoIDRe.MatchString(id) {
		return permanent(fmt.Errorf("invalid restore_id %q", id))
	}
	return nil
}

// remapper rewrites host path prefixes (longest "from" first).
func remapper(remaps []*agentv1.PathRemap) func(string) string {
	rs := make([]*agentv1.PathRemap, 0, len(remaps))
	for _, r := range remaps {
		if r.GetFrom() != "" && r.GetTo() != "" {
			rs = append(rs, r)
		}
	}
	sort.SliceStable(rs, func(i, j int) bool { return len(rs[i].From) > len(rs[j].From) })
	return func(p string) string {
		for _, r := range rs {
			from := strings.TrimSuffix(r.From, "/")
			if p == from || from == "" {
				return filepath.Join(r.To, strings.TrimPrefix(p, from))
			}
			if rest, ok := strings.CutPrefix(p, from+"/"); ok {
				return filepath.Join(r.To, rest)
			}
		}
		return p
	}
}

// engineErr classifies a repository error: missing data and local
// permission/space problems are permanent, anything else (connection) is
// retryable.
func engineErr(err error) error {
	if err == nil || isPermanent(err) {
		return err
	}
	if errors.Is(err, engine.ErrNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) ||
		errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return permanent(err)
	}
	return err
}

// restoreJob is one RestoreComponents execution.
type restoreJob struct {
	a     *Agent
	cmdID string
	c     *agentv1.RestoreComponentsCommand
	repo  engine.Repository
	j     *restoreJournal
	remap func(string) string
	swaps []*swapRecord // swaps done by this command
	done  int
}

// restoreComponents restores every component into staging, applies and
// verifies its metadata, then swaps it in (ADR-0006). Any failure rolls
// back the swaps of this command and fails it (the result lists every
// component).
func (a *Agent) restoreComponents(ctx context.Context, cmdID string, c *agentv1.RestoreComponentsCommand) (*agentv1.RestoreComponentsResult, error) {
	if err := validRestoreID(c.RestoreId); err != nil {
		return nil, err
	}
	if len(c.Components) == 0 {
		return nil, permanent(errors.New("no components to restore"))
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
	unlock := a.restores.lock(c.RestoreId)
	defer unlock()
	j, err := a.restores.open(c.RestoreId)
	if err != nil {
		return nil, err
	}
	// Undo what an interrupted attempt of this restore left half-done.
	for _, r := range j.Swaps {
		if r.State == swapStaging || r.State == swapSwapping {
			if _, err := undoSwap(r); err != nil {
				return nil, permanent(fmt.Errorf("undo interrupted swap of %s: %w", r.Target, err))
			}
		}
	}
	if err := a.restores.save(j); err != nil {
		return nil, err
	}
	repo, release, err := a.repoSession(ctx, c.RepositoryId, c.FreshSession)
	if err != nil {
		return nil, err
	}
	defer release()

	job := &restoreJob{a: a, cmdID: cmdID, c: c, repo: repo, j: j, remap: remapper(c.PathRemaps)}
	res := &agentv1.RestoreComponentsResult{}
	var failure error
	for _, s := range c.Components {
		if failure != nil {
			res.Components = append(res.Components, &agentv1.RestoreComponentResult{Name: s.Name, Status: statusSkipped,
				Error: "skipped: an earlier component failed"})
			continue
		}
		a.progress(cmdID, map[string]any{"done": job.done, "total": len(c.Components), "component": s.Name})
		r, err := job.component(ctx, s)
		job.done++
		if err != nil {
			r.Status, r.Error = statusFailed, strings.ToValidUTF8(joinNote(err.Error(), r.Error), "�")
			failure = fmt.Errorf("component %s: %w", s.Name, err)
			if ctx.Err() != nil {
				failure = fmt.Errorf("component %s: %w", s.Name, errors.Join(ctx.Err(), err))
			}
			a.log.Warn("component restore failed", "restore_id", c.RestoreId, "component", s.Name, "err", r.Error)
		}
		res.Components = append(res.Components, r)
	}
	if failure != nil {
		if err := job.rollback(); err != nil {
			failure = errors.Join(failure, fmt.Errorf("rollback: %w", err))
			a.log.Error("restore rollback failed", "restore_id", c.RestoreId, "err", err)
		}
		for _, r := range res.Components {
			if r.Status == restored {
				r.Status, r.Error, r.PreviousPath = statusSkipped, "rolled back: another component failed", ""
			}
		}
		return res, failure
	}
	a.progress(cmdID, map[string]any{"done": job.done, "total": len(c.Components)})
	return res, nil
}

// rollback undoes this command's swaps in reverse order.
func (job *restoreJob) rollback() error {
	var errs []error
	for i := len(job.swaps) - 1; i >= 0; i-- {
		if _, err := undoSwap(job.swaps[i]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", job.swaps[i].Target, err))
		}
	}
	if err := job.a.restores.save(job.j); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (job *restoreJob) save() error { return job.a.restores.save(job.j) }

// component restores one spec. A returned error fails the command; r then
// carries whatever was learned.
func (job *restoreJob) component(ctx context.Context, s *agentv1.RestoreSpec) (*agentv1.RestoreComponentResult, error) {
	r := &agentv1.RestoreComponentResult{Name: s.Name}
	if s.SnapshotId == "" {
		return r, permanent(errors.New("snapshot_id is required"))
	}
	var err error
	switch s.Kind {
	case agentv1.ComponentKind_COMPONENT_KIND_VOLUME, agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT:
		err = job.filesystem(ctx, s, r)
	case agentv1.ComponentKind_COMPONENT_KIND_CONFIG:
		err = job.config(ctx, s, r)
	default:
		r.Status, r.Error = statusSkipped, fmt.Sprintf("%s components are not restored by RestoreComponents", s.Kind)
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Status = restored
	job.a.log.Info("component restored", "restore_id", job.c.RestoreId, "component", s.Name, "target", r.TargetPath,
		"bytes", r.Bytes, "files", r.Files, "metadata_applied", r.MetadataApplied)
	return r, nil
}

// paths returns the staging, previous and discard siblings of target.
func (job *restoreJob) paths(target, suffix string) (string, string, string) {
	dir, id := filepath.Dir(target), job.c.RestoreId
	return filepath.Join(dir, ".dbr2-staging-"+id+suffix), filepath.Join(dir, ".dbr2-old-"+id+suffix),
		filepath.Join(dir, ".dbr2-discard-"+id+suffix)
}

// begin journals a new swap entry (state staging) before anything is
// written.
func (job *restoreJob) begin(component, kind, target, suffix string) (*swapRecord, error) {
	for _, r := range job.j.Swaps {
		if r.Target == target && r.State == swapSwapped {
			return nil, permanent(fmt.Errorf("%s was already restored by restore %s", target, job.c.RestoreId))
		}
	}
	staging, previous, disc := job.paths(target, suffix)
	rec := &swapRecord{Component: component, Kind: kind, Target: target, Staging: staging, Previous: previous,
		Discard: disc, State: swapStaging}
	job.j.Swaps = append(job.j.Swaps, rec)
	job.swaps = append(job.swaps, rec)
	if err := job.save(); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(staging); err != nil { // stale from an earlier attempt
		return nil, permanent(err)
	}
	return rec, nil
}

// swap moves the target aside and the staging content in, journaling
// first. It refuses when the dead-man switch already restarted the
// application stopped for this restore.
func (job *restoreJob) swap(rec *swapRecord) error {
	if job.a.leases.autoResumed(job.c.RestoreId) {
		return errLeaseExpired
	}
	rec.HadPrevious = exists(rec.Target)
	rec.State = swapSwapping
	if err := job.save(); err != nil {
		return err
	}
	if rec.HadPrevious {
		if err := os.Rename(rec.Target, rec.Previous); err != nil {
			return permanent(fmt.Errorf("move previous content aside: %w", err))
		}
	}
	if err := os.Rename(rec.Staging, rec.Target); err != nil {
		return permanent(fmt.Errorf("swap in: %w", err))
	}
	rec.State = swapSwapped
	return job.save()
}

// filesystem restores a volume or bind-mount component.
func (job *restoreJob) filesystem(ctx context.Context, s *agentv1.RestoreSpec, r *agentv1.RestoreComponentResult) error {
	var target, suffix string
	if s.Kind == agentv1.ComponentKind_COMPONENT_KIND_VOLUME {
		mp, err := job.volume(ctx, s, r)
		if err != nil {
			return err
		}
		target = mp
	} else {
		if s.TargetPath == "" || !filepath.IsAbs(s.TargetPath) {
			return permanent(fmt.Errorf("target_path %q is not absolute", s.TargetPath))
		}
		target, suffix = filepath.Clean(s.TargetPath), "-"+filepath.Base(s.TargetPath)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return permanent(err)
		}
	}
	r.TargetPath = target
	snap, err := job.repo.Get(ctx, s.SnapshotId)
	if err != nil {
		return engineErr(fmt.Errorf("snapshot %s: %w", s.SnapshotId, err))
	}
	r.Bytes, r.Files = snap.TotalBytes, snap.Files
	fileName, err := job.singleFile(ctx, s, target)
	if err != nil {
		return err
	}
	if err := freeSpace(filepath.Dir(target), snap.TotalBytes); err != nil {
		return err
	}
	kind := "dir"
	if fileName != "" {
		kind = "file"
	}
	rec, err := job.begin(s.Name, kind, target, suffix)
	if err != nil {
		return err
	}
	if fileName != "" {
		ent, err := job.rootEntry(ctx, s.SnapshotId, fileName)
		if err != nil {
			return err
		}
		// A stream entry carries no ownership of its own: the root
		// owner/mode of the spec (applied below) are authoritative.
		ent.UID, ent.GID, ent.Mode = uint32(os.Geteuid()), uint32(os.Getegid()), 0o600 //nolint:gosec // ids fit
		rc, err := job.repo.OpenStream(ctx, s.SnapshotId, fileName)
		if err != nil {
			return engineErr(err)
		}
		err = writeStaged(rec.Staging, rc, ent)
		rc.Close()
		if err != nil {
			return err
		}
		r.Files = 1
	} else {
		err := job.repo.RestorePath(ctx, s.SnapshotId, rec.Staging,
			engine.RestoreOptions{IgnorePermissionErrors: false, WriteSparseFiles: true})
		if err != nil {
			return engineErr(fmt.Errorf("restore into staging: %w", err))
		}
	}
	if err := job.metadata(ctx, s, rec.Staging, r); err != nil {
		return err
	}
	if err := job.swap(rec); err != nil {
		return err
	}
	if rec.HadPrevious {
		r.PreviousPath = rec.Previous
	}
	return nil
}

// volume resolves (or creates) the volume and returns its mountpoint.
func (job *restoreJob) volume(ctx context.Context, s *agentv1.RestoreSpec, r *agentv1.RestoreComponentResult) (string, error) {
	if s.VolumeName == "" {
		return "", permanent(errors.New("volume_name is required"))
	}
	ctl, err := job.a.restoreControl()
	if err != nil {
		return "", err
	}
	v, err := ctl.InspectVolume(ctx, s.VolumeName)
	if errors.Is(err, runtime.ErrNotFound) {
		job.j.CreatedVolumes = append(job.j.CreatedVolumes, s.VolumeName)
		if err := job.save(); err != nil {
			return "", err
		}
		v, err = ctl.CreateVolume(ctx, s.VolumeName, s.VolumeDriver, s.VolumeLabels)
		if err != nil {
			return "", fmt.Errorf("create volume %s: %w", s.VolumeName, err)
		}
		r.CreatedVolume = true
		job.a.log.Info("volume created", "restore_id", job.c.RestoreId, "volume", s.VolumeName)
	} else if err != nil {
		return "", fmt.Errorf("inspect volume %s: %w", s.VolumeName, err)
	}
	if v.Mountpoint == "" || !filepath.IsAbs(v.Mountpoint) {
		return "", permanent(fmt.Errorf("volume %s (driver %s) has no local mountpoint", s.VolumeName, v.Driver))
	}
	return filepath.Clean(v.Mountpoint), nil
}

// singleFile returns the file name of a single-stream component, or "" for
// a directory snapshot. Without file_name (manifests before Phase 5) a
// bind mount whose snapshot root holds exactly one regular file named like
// the target is a single file, unless the target is an existing directory.
func (job *restoreJob) singleFile(ctx context.Context, s *agentv1.RestoreSpec, target string) (string, error) {
	if s.FileName != "" {
		if s.FileName != path.Base(s.FileName) || s.FileName == "." || s.FileName == ".." {
			return "", permanent(fmt.Errorf("invalid file_name %q", s.FileName))
		}
		return s.FileName, nil
	}
	if s.Kind != agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT {
		return "", nil
	}
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		return "", nil
	}
	ents, err := job.repo.ListDir(ctx, s.SnapshotId, "")
	if err != nil {
		return "", engineErr(err)
	}
	if len(ents) == 1 && ents[0].IsRegular() && ents[0].Name == filepath.Base(target) {
		return ents[0].Name, nil
	}
	return "", nil
}

func (job *restoreJob) rootEntry(ctx context.Context, id, name string) (engine.DirEntry, error) {
	ents, err := job.repo.ListDir(ctx, id, "")
	if err != nil {
		return engine.DirEntry{}, engineErr(err)
	}
	for _, e := range ents {
		if e.Name == name {
			return e, nil
		}
	}
	return engine.DirEntry{}, permanent(fmt.Errorf("snapshot %s has no file %s", id, name))
}

// writeStaged writes a regular file with the entry's mode, owner and mtime.
func writeStaged(dst string, rd io.Reader, e engine.DirEntry) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return permanent(err)
	}
	if _, err := io.Copy(f, rd); err != nil {
		f.Close()
		return engineErr(fmt.Errorf("write %s: %w", dst, err))
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return permanent(err)
	}
	if err := f.Close(); err != nil {
		return permanent(err)
	}
	if err := setOwnerMode(dst, e.UID, e.GID, fileModeBits(e.Mode)); err != nil {
		return err
	}
	if err := os.Chtimes(dst, e.ModTime, e.ModTime); err != nil {
		return permanent(err)
	}
	return nil
}

// fileModeBits converts an os.FileMode to raw permission bits (with
// setuid/setgid/sticky).
func fileModeBits(m os.FileMode) uint32 {
	b := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		b |= unix.S_ISUID
	}
	if m&os.ModeSetgid != 0 {
		b |= unix.S_ISGID
	}
	if m&os.ModeSticky != 0 {
		b |= unix.S_ISVTX
	}
	return b
}

// setOwnerMode chowns then chmods (chown clears setuid/setgid).
func setOwnerMode(p string, uid, gid, mode uint32) error {
	if err := os.Lchown(p, int(uid), int(gid)); err != nil {
		return permanent(fmt.Errorf("chown %s: %w", p, err))
	}
	if err := unix.Chmod(p, mode); err != nil {
		return permanent(fmt.Errorf("chmod %s: %w", p, err))
	}
	return nil
}

// metadata applies the fsmeta record, then the recorded root ownership,
// mode and SELinux context, then verifies (ADR-0006 steps 2-6).
func (job *restoreJob) metadata(ctx context.Context, s *agentv1.RestoreSpec, staging string, r *agentv1.RestoreComponentResult) error {
	var notes []string
	if s.FsmetaSnapshotId != "" {
		err := job.fsmeta(ctx, s.FsmetaSnapshotId, func(rd *fsmeta.Reader) error {
			st, err := fsmeta.Apply(ctx, rd, staging)
			r.MetadataApplied = st.Records
			if st.Errors > 0 {
				notes = append(notes, fmt.Sprintf("%d metadata entries could not be applied (e.g. %s)", st.Errors, st.Samples[0]))
			}
			return err
		})
		if err != nil {
			return err
		}
	} else {
		job.a.log.Warn("component has no fsmeta record; hardlinks, xattrs, ACLs and directory mtimes are not restored",
			"restore_id", job.c.RestoreId, "component", s.Name)
	}
	if s.Mode != "" {
		mode, err := strconv.ParseUint(s.Mode, 8, 32)
		if err != nil {
			return permanent(fmt.Errorf("invalid mode %q", s.Mode))
		}
		if err := setOwnerMode(staging, s.OwnerUid, s.OwnerGid, uint32(mode)); err != nil {
			return err
		}
	}
	if s.SelinuxContext != "" {
		err := unix.Lsetxattr(staging, "security.selinux", []byte(s.SelinuxContext), 0)
		switch {
		case errors.Is(err, unix.ENOTSUP):
			notes = append(notes, "SELinux context not applied (unsupported on the target filesystem)")
		case err != nil:
			return permanent(fmt.Errorf("set SELinux context %s: %w", s.SelinuxContext, err))
		}
	}
	if s.FsmetaSnapshotId != "" {
		var vs fsmeta.VerifyStats
		err := job.fsmeta(ctx, s.FsmetaSnapshotId, func(rd *fsmeta.Reader) error {
			var err error
			vs, err = fsmeta.Verify(ctx, rd, staging)
			return err
		})
		if err != nil {
			return err
		}
		r.VerifyMismatches = vs.Mismatches
		if vs.Mismatches > 0 {
			r.Error = strings.Join(notes, "; ")
			return permanent(fmt.Errorf("metadata verification found %d mismatches (e.g. %s)", vs.Mismatches,
				strings.Join(vs.Samples[:min(3, len(vs.Samples))], "; ")))
		}
	}
	r.Error = strings.Join(notes, "; ")
	return nil
}

// fsmeta opens the fsmeta record snapshot and runs fn on it.
func (job *restoreJob) fsmeta(ctx context.Context, id string, fn func(*fsmeta.Reader) error) error {
	rc, err := job.repo.OpenStream(ctx, id, fsmeta.FileName)
	if err != nil {
		return engineErr(fmt.Errorf("fsmeta snapshot %s: %w", id, err))
	}
	defer rc.Close()
	rd, err := fsmeta.NewReader(rc)
	if err != nil {
		return permanent(err)
	}
	defer rd.Close()
	if err := fn(rd); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return permanent(err)
	}
	return nil
}

// freeSpace fails when dir's filesystem has less than need bytes available.
func freeSpace(dir string, need int64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return permanent(fmt.Errorf("statfs %s: %w", dir, err))
	}
	avail := int64(st.Bavail) * int64(st.Bsize) //nolint:gosec // block counts fit
	if avail < need {
		return permanent(fmt.Errorf("insufficient free space on the filesystem of %s: %d bytes available, %d needed "+
			"(the previous content is kept until the restore is finalized)", dir, avail, need))
	}
	return nil
}

// config restores the captured Compose/env files (files/<absolute path>
// in the config snapshot) to their remapped paths, one swap per file.
func (job *restoreJob) config(ctx context.Context, s *agentv1.RestoreSpec, r *agentv1.RestoreComponentResult) error {
	files, err := job.walkFiles(ctx, s.SnapshotId, "files")
	if err != nil {
		return err
	}
	for _, f := range files {
		src := "/" + strings.TrimPrefix(f.rel, "files/")
		target := filepath.Clean(job.remap(src))
		if !filepath.IsAbs(target) {
			return permanent(fmt.Errorf("remapped path %q is not absolute", target))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return permanent(err)
		}
		rec, err := job.begin(s.Name, "file", target, "-"+filepath.Base(target))
		if err != nil {
			return err
		}
		rc, err := job.repo.OpenFile(ctx, s.SnapshotId, f.rel)
		if err != nil {
			return engineErr(err)
		}
		err = writeStaged(rec.Staging, rc, f.DirEntry)
		rc.Close()
		if err != nil {
			return err
		}
		if err := job.swap(rec); err != nil {
			return err
		}
		r.Files++
		r.Bytes += f.Size
	}
	return nil
}

type snapFile struct {
	engine.DirEntry
	rel string
}

// walkFiles lists the regular files below dir of a snapshot ("" when the
// directory does not exist).
func (job *restoreJob) walkFiles(ctx context.Context, id, dir string) ([]snapFile, error) {
	ents, err := job.repo.ListDir(ctx, id, dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, engineErr(err)
	}
	var out []snapFile
	for _, e := range ents {
		rel := dir + "/" + e.Name
		switch {
		case e.IsDir():
			sub, err := job.walkFiles(ctx, id, rel)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		case e.IsRegular():
			out = append(out, snapFile{DirEntry: e, rel: rel})
		}
	}
	return out, nil
}
