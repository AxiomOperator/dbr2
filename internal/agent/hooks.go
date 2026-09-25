// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
)

const (
	defaultHookTimeout = 300 * time.Second
	maxHookOutput      = 8 << 10
)

// runHooks runs the hooks in order with docker exec. A failing required hook
// stops the sequence and fails the command; the result still lists every
// hook that ran.
func (a *Agent) runHooks(ctx context.Context, c *agentv1.RunHooksCommand) (*agentv1.RunHooksResult, error) {
	ctl, err := a.control()
	if err != nil {
		return nil, err
	}
	res := &agentv1.RunHooksResult{}
	for i, h := range c.Hooks {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		timeout := defaultHookTimeout
		if h.TimeoutSeconds > 0 {
			timeout = time.Duration(h.TimeoutSeconds) * time.Second
		}
		hr := &agentv1.HookResult{ContainerId: h.ContainerId, ExitCode: -1}
		start := time.Now()
		var herr error
		if len(h.Command) == 0 || h.ContainerId == "" {
			herr = errors.New("hook has no container or command")
		} else {
			hctx, cancel := context.WithTimeout(ctx, timeout)
			out, err := ctl.Exec(hctx, h.ContainerId, h.Command, maxHookOutput)
			timedOut := hctx.Err() == context.DeadlineExceeded && ctx.Err() == nil
			cancel()
			hr.Output = strings.ToValidUTF8(string(out.Output), "�")
			if out.Truncated {
				hr.Output = "[output truncated]\n" + hr.Output
			}
			switch {
			case timedOut:
				herr = fmt.Errorf("timed out after %s", timeout)
			case err != nil:
				herr = err
			default:
				hr.ExitCode = int32(out.ExitCode)
				if out.ExitCode != 0 {
					herr = fmt.Errorf("exit code %d", out.ExitCode)
				}
			}
		}
		hr.DurationMs = time.Since(start).Milliseconds()
		if herr != nil {
			hr.Error = strings.ToValidUTF8(herr.Error(), "�")
		}
		res.Results = append(res.Results, hr)
		a.log.Info("hook finished", "phase", c.Phase, "index", i, "container_id", h.ContainerId,
			"exit_code", hr.ExitCode, "duration_ms", hr.DurationMs, "optional", h.Optional, "err", hr.Error)
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if herr != nil && !h.Optional {
			return res, permanent(fmt.Errorf("%s hook %d in container %s failed: %w", c.Phase, i+1, h.ContainerId, herr))
		}
	}
	return res, nil
}
