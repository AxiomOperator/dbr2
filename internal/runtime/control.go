// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"errors"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

var _ ContainerControl = (*DockerRuntime)(nil)

// InspectState implements ContainerControl.
func (d *DockerRuntime) InspectState(ctx context.Context, id string) (ContainerState, error) {
	res, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerState{}, err
	}
	s := ContainerState{ID: res.Container.ID, Name: strings.TrimPrefix(res.Container.Name, "/")}
	if res.Container.State != nil {
		s.State = string(res.Container.State.Status)
	}
	return s, nil
}

// InspectRaw implements ContainerControl.
func (d *DockerRuntime) InspectRaw(ctx context.Context, id string) ([]byte, error) {
	res, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}
	return res.Raw, nil
}

// Pause implements ContainerControl.
func (d *DockerRuntime) Pause(ctx context.Context, id string) error {
	_, err := d.cli.ContainerPause(ctx, id, client.ContainerPauseOptions{})
	return err
}

// Unpause implements ContainerControl.
func (d *DockerRuntime) Unpause(ctx context.Context, id string) error {
	_, err := d.cli.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
	return err
}

// Stop implements ContainerControl.
func (d *DockerRuntime) Stop(ctx context.Context, id string) error {
	_, err := d.cli.ContainerStop(ctx, id, client.ContainerStopOptions{})
	return err
}

// Start implements ContainerControl.
func (d *DockerRuntime) Start(ctx context.Context, id string) error {
	_, err := d.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// Exec implements ContainerControl. On ctx expiry the attach is closed; the
// engine offers no way to kill an exec, so the process may keep running.
func (d *DockerRuntime) Exec(ctx context.Context, id string, cmd []string, maxOutput int) (ExecResult, error) {
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
	out := NewTailBuffer(maxOutput)
	_, cerr := stdcopy.StdCopy(out, out, at.Reader)
	if ctx.Err() != nil {
		return ExecResult{Output: out.Bytes(), Truncated: out.Truncated()}, ctx.Err()
	}
	if cerr != nil {
		return ExecResult{Output: out.Bytes(), Truncated: out.Truncated()}, cerr
	}
	ins, err := d.cli.ExecInspect(ctx, cr.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{Output: out.Bytes(), Truncated: out.Truncated()}, err
	}
	return ExecResult{ExitCode: ins.ExitCode, Output: out.Bytes(), Truncated: out.Truncated()}, nil
}

// TailBuffer is an io.Writer keeping only the last n bytes written.
type TailBuffer struct {
	n         int
	buf       []byte
	truncated bool
}

// NewTailBuffer returns a TailBuffer of n bytes (n <= 0 = 8 KiB).
func NewTailBuffer(n int) *TailBuffer {
	if n <= 0 {
		n = 8 << 10
	}
	return &TailBuffer{n: n}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.n; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.truncated = true
	}
	return len(p), nil
}

// Bytes returns the kept bytes.
func (t *TailBuffer) Bytes() []byte { return t.buf }

// Truncated reports whether earlier output was dropped.
func (t *TailBuffer) Truncated() bool { return t.truncated }
