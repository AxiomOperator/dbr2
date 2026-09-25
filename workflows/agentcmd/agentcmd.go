// SPDX-License-Identifier: Apache-2.0

// Package agentcmd dispatches agent commands from Temporal activities
// through the Agent Gateway control channel (ADR-0001), relaying agent
// progress as activity heartbeats.
package agentcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
)

// Non-retryable application error types.
const (
	ErrAgentNotActive     = "AgentNotActive"
	ErrAgentCommandFailed = "AgentCommandFailed"
)

// Dispatcher sends commands to agents.
type Dispatcher struct {
	Control controlv1.GatewayControlServiceClient
	Token   string
	// WaitForAgent is how long the gateway waits for an offline agent.
	WaitForAgent time.Duration
}

// CommandID is stable across retries of the calling activity in this run,
// so a retried attempt gets the agent's journaled result (ADR-0001).
func CommandID(ctx context.Context) string {
	info := activity.GetInfo(ctx)
	return info.WorkflowExecution.ID + "/" + info.WorkflowExecution.RunID + "/" + info.ActivityID
}

// Dispatch sends cmd and waits for its terminal update. onProgress (may be
// nil) receives RUNNING updates carrying progress.
func (d *Dispatcher) Dispatch(ctx context.Context, agentID string, cmd *agentv1.Command, onProgress func(*agentv1.CommandUpdate)) (*agentv1.CommandUpdate, error) {
	wait := d.WaitForAgent
	if wait == 0 {
		wait = 5 * time.Minute
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+d.Token)
	stream, err := d.Control.Dispatch(ctx, &controlv1.DispatchRequest{AgentId: agentID, Command: cmd, WaitForAgentSeconds: uint32(wait / time.Second)})
	if err != nil {
		return nil, err
	}
	// Heartbeat on a ticker too: the stream may be quiet while the gateway
	// waits for a reconnecting agent.
	hbCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx, "waiting")
			}
		}
	}()
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("gateway closed the dispatch stream without a result")
		}
		if err != nil {
			if status.Code(err) == codes.FailedPrecondition {
				return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrAgentNotActive, err)
			}
			return nil, err
		}
		u := resp.Update
		activity.RecordHeartbeat(ctx, u.State.String())
		switch u.State {
		case agentv1.CommandState_COMMAND_STATE_SUCCEEDED:
			return u, nil
		case agentv1.CommandState_COMMAND_STATE_FAILED:
			e := fmt.Errorf("agent command failed: %s", u.Error)
			if !u.Retryable {
				return nil, temporal.NewNonRetryableApplicationError(e.Error(), ErrAgentCommandFailed, e)
			}
			return nil, e
		default:
			if onProgress != nil && len(u.Progress) > 0 {
				onProgress(u)
			}
		}
	}
}
