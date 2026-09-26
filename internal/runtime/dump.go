// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// DumpControl is implemented by runtimes that can capture database dumps
// (Phase 8): stream a command's stdout out of a container and copy files
// out of it. The agent type-asserts for it.
type DumpControl interface {
	RestoreControl
	// ExecOutput runs cmd in the container (no shell), streaming its stdout
	// into stdout; ExecResult.Output holds the last maxStderr bytes of
	// stderr. A write error on stdout aborts the exec and is returned. A
	// non-zero exit is not an error.
	ExecOutput(ctx context.Context, id string, cmd []string, stdout io.Writer, maxStderr int) (ExecResult, error)
	// CopyFromContainer returns a tar stream of srcPath (a file or a
	// directory, named by its base name inside the archive). The caller
	// closes it.
	CopyFromContainer(ctx context.Context, id, srcPath string) (io.ReadCloser, error)
}

var _ DumpControl = (*DockerRuntime)(nil)

// ExecOutput implements DumpControl.
func (d *DockerRuntime) ExecOutput(ctx context.Context, id string, cmd []string, stdout io.Writer, maxStderr int) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, errors.New("empty command")
	}
	cr, err := d.cli.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return ExecResult{}, err
	}
	at, err := d.cli.ExecAttach(ctx, cr.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, err
	}
	defer at.Close()
	stop := context.AfterFunc(ctx, at.Close)
	defer stop()
	errOut := NewTailBuffer(maxStderr)
	_, cerr := stdcopy.StdCopy(stdout, errOut, at.Reader)
	res := ExecResult{Output: errOut.Bytes(), Truncated: errOut.Truncated()}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if cerr != nil {
		return res, cerr
	}
	// The exit code decides whether a dump is complete: wait until the
	// engine has recorded it (the stream can close slightly earlier).
	for i := 0; ; i++ {
		ins, err := d.cli.ExecInspect(ctx, cr.ID, client.ExecInspectOptions{})
		if err != nil {
			return res, err
		}
		if !ins.Running {
			res.ExitCode = ins.ExitCode
			return res, nil
		}
		if i >= 100 {
			return res, errors.New("exec output ended but the process is still running")
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// CopyFromContainer implements DumpControl.
func (d *DockerRuntime) CopyFromContainer(ctx context.Context, id, srcPath string) (io.ReadCloser, error) {
	res, err := d.cli.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: srcPath})
	if err != nil {
		return nil, notFound(err)
	}
	return res.Content, nil
}
