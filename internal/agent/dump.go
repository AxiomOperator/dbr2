// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// Phase 8 database dump capture (ADR-0017 formats). Dumps are taken online
// inside the database container; the worker runs them before quiescing.
const (
	pgDumpFile  = "dump.sql.zst"
	rdbDumpFile = "dump.rdb"
	// pgTrailer ends every complete pg_dumpall output.
	pgTrailer = "-- PostgreSQL database cluster dump complete"
	pgTailLen = 1024
)

var (
	// redisSaveTimeout bounds the wait for BGSAVE (variables for tests).
	redisSaveTimeout  = 30 * time.Minute
	redisPollInterval = 500 * time.Millisecond

	errRedisAuth = errors.New("Redis AUTH is not supported yet (the server requires a password)") //nolint:staticcheck // proper noun
	rdbMagicRe   = regexp.MustCompile(`^REDIS[0-9]{4}$`)
)

// dumpControl returns the runtime's dump capability.
func (a *Agent) dumpControl() (runtime.DumpControl, error) {
	if a.rt == nil {
		return nil, errors.New("container runtime unavailable")
	}
	c, ok := a.rt.(runtime.DumpControl)
	if !ok {
		return nil, errors.New("container runtime " + a.rt.Name() + " cannot capture database dumps")
	}
	return c, nil
}

// database captures a DATABASE component as a logical dump.
func (j *snapshotJob) database(ctx context.Context, s *agentv1.ComponentSpec, r *agentv1.ComponentResult) (*engine.Snapshot, error) {
	d := s.Database
	if d == nil {
		return nil, errors.New("database component has no database spec")
	}
	r.Database = d
	if d.ContainerId == "" {
		return nil, errors.New("database spec has no container_id")
	}
	var dump func(context.Context, runtime.DumpControl, *agentv1.ComponentSpec, *agentv1.ComponentResult, runtime.ContainerDetails) (*engine.Snapshot, error)
	switch d.Engine + "/" + d.Format {
	case enginePostgres + "/" + formatPGDump:
		dump = j.dumpPostgres
	case engineRedis + "/" + formatRDB:
		dump = j.dumpRedis
	default:
		return nil, fmt.Errorf("unsupported database dump %s/%s", d.Engine, d.Format)
	}
	ctl, err := j.a.dumpControl()
	if err != nil {
		return nil, err
	}
	det, err := ctl.InspectDetails(ctx, d.ContainerId)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", d.ContainerId, err)
	}
	if det.State != "running" {
		return nil, fmt.Errorf("container %s is %s; a dump needs it running", det.Name, det.State)
	}
	return dump(ctx, ctl, s, r, det)
}

// tailWriter counts what passes through and keeps the last n bytes.
type tailWriter struct {
	n    int64
	tail *runtime.TailBuffer
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.n += int64(len(p))
	return t.tail.Write(p)
}

// pgValidate checks a finished pg_dumpall: exit 0 and the cluster trailer
// at the end of the uncompressed stream.
func pgValidate(out runtime.ExecResult, tw *tailWriter) error {
	if out.ExitCode != 0 {
		msg := strings.TrimSpace(strings.ToValidUTF8(string(out.Output), "�"))
		return fmt.Errorf("pg_dumpall exited with code %d: %s", out.ExitCode, msg)
	}
	if !bytes.Contains(tw.tail.Bytes(), []byte(pgTrailer)) {
		return fmt.Errorf("pg_dumpall output is incomplete: the trailer %q is missing (%d bytes)", pgTrailer, tw.n)
	}
	return nil
}

// dumpPostgres streams `pg_dumpall` through zstd into a stream snapshot.
// The dump is validated while it streams; a failed validation fails the
// stream, so no snapshot is saved.
func (j *snapshotJob) dumpPostgres(ctx context.Context, ctl runtime.DumpControl, s *agentv1.ComponentSpec, r *agentv1.ComponentResult, det runtime.ContainerDetails) (*engine.Snapshot, error) {
	user := s.Database.User
	if user == "" {
		user = envValue(det.Env, "POSTGRES_USER")
	}
	if user == "" {
		user = "postgres"
	}
	pr, pw := io.Pipe()
	zw, err := zstd.NewWriter(pw)
	if err != nil {
		return nil, err
	}
	tw := &tailWriter{tail: runtime.NewTailBuffer(pgTailLen)}
	done := make(chan error, 1)
	go func() {
		out, err := ctl.ExecOutput(ctx, s.Database.ContainerId,
			[]string{"pg_dumpall", "--clean", "--if-exists", "-U", user}, io.MultiWriter(tw, zw), maxDBOutput)
		if err != nil {
			err = fmt.Errorf("pg_dumpall: %w", err)
		} else {
			err = pgValidate(out, tw)
		}
		if err != nil {
			pw.CloseWithError(err) // before Close: the final frame must not complete the stream
			_ = zw.Close()
		} else {
			pw.CloseWithError(zw.Close())
		}
		done <- err
	}()
	snap, err := j.repo.SnapshotStream(ctx, pgDumpFile, pr, j.request(s.Name, manifest.KindDatabase))
	pr.CloseWithError(errors.New("snapshot finished"))
	if derr := <-done; derr != nil {
		return nil, errors.Join(derr, ignoreReadStream(err, derr))
	}
	if err != nil {
		return nil, err
	}
	r.FileName = pgDumpFile
	r.Validation = fmt.Sprintf("pg_dumpall exit 0, trailer present, %s uncompressed", humanBytes(tw.n))
	return snap, nil
}

// ignoreReadStream drops the engine's echo of the stream's own error.
func ignoreReadStream(err, cause error) error {
	if err == nil || errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error()) {
		return nil
	}
	return err
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// redisCmd runs redis-cli, mapping an authentication demand to errRedisAuth.
func redisCmd(ctx context.Context, ctl runtime.RestoreControl, id string, args ...string) ([]string, error) {
	lines, err := redisCLI(ctx, ctl, id, args...)
	for _, l := range lines {
		if strings.Contains(l, "NOAUTH") || strings.Contains(l, "Authentication required") {
			return lines, errRedisAuth
		}
	}
	if err != nil {
		return lines, err
	}
	if len(lines) > 0 && (strings.HasPrefix(lines[0], "ERR") || strings.HasPrefix(lines[0], "(error)")) {
		return lines, fmt.Errorf("redis-cli %s: %s", strings.Join(args, " "), strings.Join(lines, " "))
	}
	return lines, nil
}

func redisInt(ctx context.Context, ctl runtime.RestoreControl, id string, args ...string) (int64, error) {
	lines, err := redisCmd(ctx, ctl, id, args...)
	if err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return 0, fmt.Errorf("redis-cli %s: empty reply", strings.Join(args, " "))
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(lines[0], "(integer) "), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("redis-cli %s: unexpected reply %q", strings.Join(args, " "), lines[0])
	}
	return n, nil
}

// redisInfo returns the key:value fields of INFO <section>.
func redisInfo(ctx context.Context, ctl runtime.RestoreControl, id, section string) (map[string]string, error) {
	lines, err := redisCmd(ctx, ctl, id, "INFO", section)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range lines {
		if k, v, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(l, "#") {
			m[k] = v
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("INFO %s: empty reply", section)
	}
	return m, nil
}

// redisConfigOpt reads a CONFIG GET value that may not exist ("" then).
func redisConfigOpt(ctx context.Context, ctl runtime.RestoreControl, id, name string) (string, error) {
	lines, err := redisCmd(ctx, ctl, id, "CONFIG", "GET", name)
	if err != nil {
		return "", err
	}
	if len(lines) < 2 || lines[0] != name {
		return "", nil
	}
	return lines[1], nil
}

func redisConfigReq(ctx context.Context, ctl runtime.RestoreControl, id, name string) (string, error) {
	v, err := redisConfigOpt(ctx, ctl, id, name)
	if err == nil && v == "" {
		err = fmt.Errorf("CONFIG GET %s returned nothing", name)
	}
	return v, err
}

// redisBGSave triggers a background save and waits until a save newer than
// the call finished successfully.
func redisBGSave(ctx context.Context, ctl runtime.RestoreControl, id string) error {
	before, err := redisInt(ctx, ctl, id, "LASTSAVE")
	if err != nil {
		return err
	}
	info, err := redisInfo(ctx, ctl, id, "persistence")
	if err != nil {
		return err
	}
	savesBefore, haveSaves := int64(0), false
	if v, ok := info["rdb_saves"]; ok {
		savesBefore, err = strconv.ParseInt(v, 10, 64)
		haveSaves = err == nil
	}
	if !haveSaves {
		// LASTSAVE has a one-second resolution: make sure a save started
		// now cannot end within the second of the previous one.
		if now, err := redisCmd(ctx, ctl, id, "TIME"); err == nil && len(now) > 0 {
			if sec, err := strconv.ParseInt(now[0], 10, 64); err == nil && sec <= before {
				if err := sleepCtx(ctx, time.Until(time.Unix(before+1, 0))+100*time.Millisecond); err != nil {
					return err
				}
			}
		}
	}
	lines, err := redisCmd(ctx, ctl, id, "BGSAVE")
	reply := strings.Join(lines, " ")
	switch {
	case errors.Is(err, errRedisAuth):
		return err
	case strings.Contains(reply, "already in progress"):
		// Wait for it: it finishes after this call started.
	case strings.Contains(reply, "rewriting in progress"):
		if _, err := redisCmd(ctx, ctl, id, "BGSAVE", "SCHEDULE"); err != nil {
			return err
		}
	case err != nil:
		return err
	case !strings.Contains(reply, "Background saving"):
		return fmt.Errorf("unexpected BGSAVE reply %q", reply)
	}
	deadline := time.Now().Add(redisSaveTimeout)
	for {
		info, err := redisInfo(ctx, ctl, id, "persistence")
		if err != nil {
			return err
		}
		if info["rdb_bgsave_in_progress"] == "0" {
			advanced := false
			if n, err := strconv.ParseInt(info["rdb_saves"], 10, 64); haveSaves && err == nil {
				advanced = n > savesBefore
			} else if last, err := redisInt(ctx, ctl, id, "LASTSAVE"); err == nil {
				advanced = last > before
			} else {
				return err
			}
			status := info["rdb_last_bgsave_status"]
			switch {
			case advanced && status == "ok":
				return nil
			case status == "err" && info["aof_rewrite_in_progress"] != "1":
				return errors.New("BGSAVE failed (rdb_last_bgsave_status:err; see the Redis log)")
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("BGSAVE did not complete within %s", redisSaveTimeout)
		}
		if err := sleepCtx(ctx, redisPollInterval); err != nil {
			return err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// rdbReader passes an RDB file through, failing the stream when it is
// empty or does not start with the RDB magic (REDIS + 4 digits).
type rdbReader struct {
	r     io.Reader
	head  []byte
	n     int64
	magic string
}

func (v *rdbReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	v.n += int64(n)
	if len(v.head) < 9 {
		v.head = append(v.head, p[:min(n, 9-len(v.head))]...)
		if len(v.head) == 9 {
			if !rdbMagicRe.Match(v.head) {
				return n, fmt.Errorf("not an RDB file (header %q)", v.head)
			}
			v.magic = string(v.head)
		}
	}
	if errors.Is(err, io.EOF) && v.magic == "" {
		if v.n == 0 {
			return n, errors.New("the RDB file is empty")
		}
		return n, fmt.Errorf("the RDB file is truncated (%d bytes)", v.n)
	}
	return n, err
}

// copyOut opens the single regular file srcPath of the container.
func copyOut(ctx context.Context, ctl runtime.DumpControl, id, srcPath string) (io.Reader, func(), error) {
	rc, err := ctl.CopyFromContainer(ctx, id, srcPath)
	if err != nil {
		return nil, nil, fmt.Errorf("copy %s out of the container: %w", srcPath, err)
	}
	tr := tar.NewReader(rc)
	h, err := tr.Next()
	if err != nil {
		rc.Close()
		return nil, nil, fmt.Errorf("copy %s: %w", srcPath, err)
	}
	if h.Typeflag != tar.TypeReg {
		rc.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file", srcPath)
	}
	return tr, func() { rc.Close() }, nil
}

// dumpRedis runs BGSAVE, then captures the RDB file (and, when asked and
// enabled, the AOF).
func (j *snapshotJob) dumpRedis(ctx context.Context, ctl runtime.DumpControl, s *agentv1.ComponentSpec, r *agentv1.ComponentResult, _ runtime.ContainerDetails) (*engine.Snapshot, error) {
	id := s.Database.ContainerId
	if err := redisBGSave(ctx, ctl, id); err != nil {
		return nil, err
	}
	dir, err := redisConfigReq(ctx, ctl, id, "dir")
	if err != nil {
		return nil, err
	}
	file, err := redisConfigReq(ctx, ctl, id, "dbfilename")
	if err != nil {
		return nil, err
	}
	if !path.IsAbs(dir) || file != path.Base(file) {
		return nil, fmt.Errorf("unexpected redis dir %q / dbfilename %q", dir, file)
	}
	aofNote := ""
	var aofPath string
	if s.Database.IncludeAof {
		aof, err := redisConfigReq(ctx, ctl, id, "appendonly")
		if err != nil {
			return nil, err
		}
		if aof != "yes" {
			aofNote = "; AOF disabled, RDB only"
		} else {
			name, err := redisConfigOpt(ctx, ctl, id, "appenddirname") // Redis ≥ 7
			if err != nil {
				return nil, err
			}
			if name == "" {
				if name, err = redisConfigReq(ctx, ctl, id, "appendfilename"); err != nil {
					return nil, err
				}
			}
			if name != path.Base(name) || name == "." || name == ".." {
				return nil, fmt.Errorf("unexpected AOF name %q", name)
			}
			aofPath = path.Join(dir, name)
		}
	}
	src, closeSrc, err := copyOut(ctx, ctl, id, path.Join(dir, file))
	if err != nil {
		return nil, err
	}
	defer closeSrc()
	rdb := &rdbReader{r: src}
	req := j.request(s.Name, manifest.KindDatabase)
	var snap *engine.Snapshot
	if aofPath == "" {
		snap, err = j.repo.SnapshotStream(ctx, rdbDumpFile, rdb, req)
		if err != nil {
			return nil, err
		}
	} else {
		stage, err := j.stageDir("redis-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(stage)
		if err := writeSpool(filepath.Join(stage, rdbDumpFile), rdb); err != nil {
			return nil, err
		}
		n, size, err := copyTree(ctx, ctl, id, aofPath, stage)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("AOF %s holds no files", aofPath)
		}
		aofNote = fmt.Sprintf("; AOF %s (%d files, %s)", path.Base(aofPath), n, humanBytes(size))
		if snap, err = j.repo.SnapshotPath(ctx, stage, req); err != nil {
			return nil, err
		}
	}
	r.FileName = rdbDumpFile
	r.Validation = fmt.Sprintf("BGSAVE ok, RDB magic %s, %s%s", rdb.magic, humanBytes(rdb.n), aofNote)
	return snap, nil
}

func (j *snapshotJob) stageDir(prefix string) (string, error) {
	base := j.a.cfg.path(tmpDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, prefix)
}

func writeSpool(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// copyTree extracts the tar of srcPath (a file or directory) into stage,
// accepting only regular files and directories below the archive's top
// entry. It returns the number of files and bytes.
func copyTree(ctx context.Context, ctl runtime.DumpControl, id, srcPath, stage string) (int, int64, error) {
	rc, err := ctl.CopyFromContainer(ctx, id, srcPath)
	if err != nil {
		return 0, 0, fmt.Errorf("copy %s out of the container: %w", srcPath, err)
	}
	defer rc.Close()
	top := path.Base(srcPath)
	tr := tar.NewReader(rc)
	n, size := 0, int64(0)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return n, size, nil
		}
		if err != nil {
			return n, size, fmt.Errorf("copy %s: %w", srcPath, err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name != top && !strings.HasPrefix(name, top+"/") || strings.Contains(name, "..") {
			return n, size, fmt.Errorf("copy %s: unexpected archive entry %q", srcPath, h.Name)
		}
		dst := filepath.Join(stage, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return n, size, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return n, size, err
			}
			if err := writeSpool(dst, tr); err != nil {
				return n, size, err
			}
			n++
			size += h.Size
		default:
			return n, size, fmt.Errorf("copy %s: %q is not a regular file or directory", srcPath, h.Name)
		}
	}
}
