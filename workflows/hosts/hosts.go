// SPDX-License-Identifier: Apache-2.0

// Package hosts contains host-level workflows (Phase 2–3): commands are
// dispatched to agents through the Agent Gateway's control channel
// (ADR-0001), with Temporal heartbeats relaying agent progress.
package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
)

// DiscoverInput selects the host.
type DiscoverInput struct {
	AgentID string
}

// DiscoverResult summarizes a discovery.
type DiscoverResult struct {
	CommandID      string
	InventoryBytes int
}

// DiscoverHost runs a discovery on one host (Phase 3).
func DiscoverHost(ctx workflow.Context, in DiscoverInput) (DiscoverResult, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		// Longer than the gateway's reconnect grace, so a brief agent
		// disconnect does not fail the activity (ADR-0001 amendment).
		HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 5 * time.Second, MaximumAttempts: 5,
			NonRetryableErrorTypes: []string{ErrAgentNotActive},
		},
	})
	var a *Activities
	var res DiscoverResult
	err := workflow.ExecuteActivity(ctx, a.Discover, in.AgentID).Get(ctx, &res)
	return res, err
}

// ErrAgentNotActive is the non-retryable error type for non-active agents.
const ErrAgentNotActive = "AgentNotActive"

// Activities dispatch commands through the gateway control channel.
type Activities struct {
	Control controlv1.GatewayControlServiceClient
	Token   string
	// WaitForAgent is how long the gateway waits for an offline agent.
	WaitForAgent time.Duration
}

// Discover asks the agent for its inventory; the gateway ingests it.
func (a *Activities) Discover(ctx context.Context, agentID string) (DiscoverResult, error) {
	info := activity.GetInfo(ctx)
	// Stable across retries of this activity in this run, so a retried
	// attempt gets the agent's journaled result instead of a second run.
	commandID := info.WorkflowExecution.ID + "/" + info.WorkflowExecution.RunID + "/" + info.ActivityID
	cmd := &agentv1.Command{CommandId: commandID, DeadlineUnixMs: time.Now().Add(15 * time.Minute).UnixMilli(),
		Kind: &agentv1.Command_Discover{Discover: &agentv1.DiscoverCommand{IncludeFilesystemChanges: true}}}
	final, err := a.dispatch(ctx, agentID, cmd)
	if err != nil {
		return DiscoverResult{}, err
	}
	return DiscoverResult{CommandID: commandID, InventoryBytes: len(final.GetDiscover().GetInventoryJson())}, nil
}

func (a *Activities) dispatch(ctx context.Context, agentID string, cmd *agentv1.Command) (*agentv1.CommandUpdate, error) {
	wait := a.WaitForAgent
	if wait == 0 {
		wait = 5 * time.Minute
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.Token)
	stream, err := a.Control.Dispatch(ctx, &controlv1.DispatchRequest{AgentId: agentID, Command: cmd, WaitForAgentSeconds: uint32(wait / time.Second)})
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
				return nil, temporal.NewNonRetryableApplicationError(e.Error(), "AgentCommandFailed", e)
			}
			return nil, e
		}
	}
}
