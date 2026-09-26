// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// Database dump formats (manifest.DatabaseDump).
const (
	enginePostgres = "postgresql"
	engineRedis    = "redis"
	formatPGDump   = "pg_dumpall-sql-zstd"
	formatRDB      = "rdb"

	maxDBOutput   = 8 << 10
	redisReadyFor = 2 * time.Minute
)

// restoreDatabase loads a logical dump into a running container.
func (a *Agent) restoreDatabase(ctx context.Context, cmdID string, c *agentv1.RestoreDatabaseCommand) (*agentv1.RestoreDatabaseResult, error) {
	if err := validRestoreID(c.RestoreId); err != nil {
		return nil, err
	}
	switch {
	case c.SnapshotId == "" || c.ContainerId == "":
		return nil, permanent(errors.New("snapshot_id and container_id are required"))
	case c.FileName == "" || c.FileName != path.Base(c.FileName):
		return nil, permanent(fmt.Errorf("invalid file_name %q", c.FileName))
	}
	load := map[string]func(context.Context, runtime.RestoreControl, engine.Repository, *agentv1.RestoreDatabaseCommand) (*agentv1.RestoreDatabaseResult, error){
		enginePostgres + "/" + formatPGDump: a.loadPostgres,
		engineRedis + "/" + formatRDB:       a.loadRedis,
	}[c.Engine+"/"+c.Format]
	if load == nil {
		return nil, permanent(fmt.Errorf("unsupported database dump %s/%s", c.Engine, c.Format))
	}
	if _, err := a.loadConnection(c.RepositoryId); err != nil {
		return nil, err
	}
	ctl, err := a.restoreControl()
	if err != nil {
		return nil, err
	}
	if !a.jobs.tryAcquire() {
		a.progress(cmdID, map[string]any{"queued": true})
		if err := a.jobs.acquire(ctx); err != nil {
			return nil, err
		}
	}
	defer a.jobs.release()
	repo, release, err := a.repoSession(ctx, c.RepositoryId, c.FreshSession)
	if err != nil {
		return nil, err
	}
	defer release()
	d, err := ctl.InspectDetails(ctx, c.ContainerId)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", c.ContainerId, err)
	}
	if d.State != "running" {
		return nil, permanent(fmt.Errorf("container %s is %s; it must be running", d.Name, d.State))
	}
	a.progress(cmdID, map[string]any{"component": c.Name, "engine": c.Engine})
	res, err := load(ctx, ctl, repo, c)
	if err != nil {
		a.log.Warn("database restore failed", "restore_id", c.RestoreId, "component", c.Name, "err", err)
		return res, err
	}
	a.log.Info("database restored", "restore_id", c.RestoreId, "component", c.Name, "engine", c.Engine, "bytes", res.Bytes)
	return res, nil
}

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// loadPostgres streams the zstd-compressed pg_dumpall SQL into psql. Once
// the load started a failure is permanent (a repeat would load twice).
func (a *Agent) loadPostgres(ctx context.Context, ctl runtime.RestoreControl, repo engine.Repository, c *agentv1.RestoreDatabaseCommand) (*agentv1.RestoreDatabaseResult, error) {
	d, err := ctl.InspectDetails(ctx, c.ContainerId)
	if err != nil {
		return nil, err
	}
	user := envValue(d.Env, "POSTGRES_USER")
	if user == "" {
		user = "postgres"
	}
	rc, err := repo.OpenStream(ctx, c.SnapshotId, c.FileName)
	if err != nil {
		return nil, engineErr(err)
	}
	defer rc.Close()
	zr, err := zstd.NewReader(rc)
	if err != nil {
		return nil, permanent(err)
	}
	defer zr.Close()
	in := &countingReader{r: zr}
	out, err := ctl.ExecInput(ctx, c.ContainerId, []string{"psql", "-X", "-U", user, "-d", "postgres"}, in, maxDBOutput)
	res := &agentv1.RestoreDatabaseResult{Bytes: in.n.Load(), Output: strings.ToValidUTF8(string(out.Output), "�")}
	if err != nil {
		if ctx.Err() != nil {
			return res, err
		}
		return res, permanent(fmt.Errorf("psql: %w", err))
	}
	if out.ExitCode != 0 {
		return res, permanent(fmt.Errorf("psql exited with code %d", out.ExitCode))
	}
	return res, nil
}

// redisCLI runs redis-cli and returns its trimmed output lines.
func redisCLI(ctx context.Context, ctl runtime.RestoreControl, id string, args ...string) ([]string, error) {
	out, err := ctl.Exec(ctx, id, append([]string{"redis-cli"}, args...), maxDBOutput)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(out.Output), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if out.ExitCode != 0 {
		return lines, fmt.Errorf("redis-cli %s exited with code %d: %s", strings.Join(args, " "), out.ExitCode, strings.Join(lines, " "))
	}
	return lines, nil
}

func redisConfig(ctx context.Context, ctl runtime.RestoreControl, id, name string) (string, error) {
	lines, err := redisCLI(ctx, ctl, id, "CONFIG", "GET", name)
	if err != nil {
		return "", err
	}
	if len(lines) < 2 || lines[0] != name {
		return "", fmt.Errorf("unexpected CONFIG GET %s reply: %q", name, strings.Join(lines, " "))
	}
	return lines[1], nil
}

// loadRedis replaces the RDB file while the container is stopped, then
// starts it and waits for PING.
func (a *Agent) loadRedis(ctx context.Context, ctl runtime.RestoreControl, repo engine.Repository, c *agentv1.RestoreDatabaseCommand) (*agentv1.RestoreDatabaseResult, error) {
	id := c.ContainerId
	dir, err := redisConfig(ctx, ctl, id, "dir")
	if err != nil {
		return nil, permanent(err)
	}
	file, err := redisConfig(ctx, ctl, id, "dbfilename")
	if err != nil {
		return nil, permanent(err)
	}
	if aof, err := redisConfig(ctx, ctl, id, "appendonly"); err != nil {
		return nil, permanent(err)
	} else if aof == "yes" {
		return nil, permanent(errors.New("AOF enabled: restore the volume component instead"))
	}
	if !path.IsAbs(dir) || file != path.Base(file) {
		return nil, permanent(fmt.Errorf("unexpected redis dir %q / dbfilename %q", dir, file))
	}
	// Ownership and mode of the current dump (else of the directory).
	uid, gid, mode := 0, 0, int64(0o644)
	for _, p := range []string{path.Join(dir, file), dir} {
		out, err := ctl.Exec(ctx, id, []string{"stat", "-c", "%u:%g:%a", p}, 256)
		if err != nil || out.ExitCode != 0 {
			continue
		}
		f := strings.Split(strings.TrimSpace(string(out.Output)), ":")
		if len(f) == 3 {
			uid, _ = strconv.Atoi(f[0])
			gid, _ = strconv.Atoi(f[1])
			if p != dir {
				mode, _ = strconv.ParseInt(f[2], 8, 32)
			}
		}
		break
	}

	// Spool the dump (a tar header needs the size).
	if err := os.MkdirAll(a.cfg.path(tmpDir), 0o700); err != nil {
		return nil, err
	}
	spool, err := os.CreateTemp(a.cfg.path(tmpDir), "rdb-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	rc, err := repo.OpenStream(ctx, c.SnapshotId, c.FileName)
	if err != nil {
		return nil, engineErr(err)
	}
	size, err := io.Copy(spool, rc)
	rc.Close()
	if err != nil {
		return nil, engineErr(fmt.Errorf("read dump: %w", err))
	}
	res := &agentv1.RestoreDatabaseResult{Bytes: size}

	if err := ctl.Stop(ctx, id); err != nil {
		return res, fmt.Errorf("stop %s: %w", id, err)
	}
	copyErr := func() error {
		if _, err := spool.Seek(0, io.SeekStart); err != nil {
			return err
		}
		pr, pw := io.Pipe()
		go func() {
			tw := tar.NewWriter(pw)
			err := tw.WriteHeader(&tar.Header{Name: file, Mode: mode, Size: size, Uid: uid, Gid: gid,
				ModTime: time.Now(), Typeflag: tar.TypeReg})
			if err == nil {
				_, err = io.Copy(tw, spool)
			}
			if err == nil {
				err = tw.Close()
			}
			pw.CloseWithError(err)
		}()
		err := ctl.CopyToContainer(ctx, id, dir, pr)
		pr.CloseWithError(errors.New("copy finished"))
		return err
	}()
	// Start the container whatever happened: it was running before.
	startErr := ctl.Start(context.WithoutCancel(ctx), id)
	if copyErr != nil {
		return res, permanent(errors.Join(fmt.Errorf("copy dump into %s: %w", id, copyErr), startErr))
	}
	if startErr != nil {
		return res, permanent(fmt.Errorf("start %s: %w", id, startErr))
	}
	deadline := time.Now().Add(redisReadyFor)
	for {
		lines, err := redisCLI(ctx, ctl, id, "PING")
		if err == nil && len(lines) > 0 && lines[0] == "PONG" {
			res.Output = "PONG"
			return res, nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("PING replied %q", strings.Join(lines, " "))
			}
			return res, permanent(fmt.Errorf("redis not ready after the restore: %w", err))
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
