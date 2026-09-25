// SPDX-License-Identifier: Apache-2.0

// Package hosts contains host-level workflows (Phase 2–3): commands are
// dispatched to agents through the Agent Gateway's control channel
// (ADR-0001), with Temporal heartbeats relaying agent progress.
package hosts

import (
	"context"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/workflows/agentcmd"
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
const ErrAgentNotActive = agentcmd.ErrAgentNotActive

// Activities dispatch commands through the gateway control channel.
type Activities struct {
	Control controlv1.GatewayControlServiceClient
	Token   string
	// WaitForAgent is how long the gateway waits for an offline agent.
	WaitForAgent time.Duration
}

// Discover asks the agent for its inventory; the gateway ingests it.
func (a *Activities) Discover(ctx context.Context, agentID string) (DiscoverResult, error) {
	commandID := agentcmd.CommandID(ctx)
	cmd := &agentv1.Command{CommandId: commandID, DeadlineUnixMs: time.Now().Add(15 * time.Minute).UnixMilli(),
		Kind: &agentv1.Command_Discover{Discover: &agentv1.DiscoverCommand{IncludeFilesystemChanges: true}}}
	final, err := a.dispatch(ctx, agentID, cmd)
	if err != nil {
		return DiscoverResult{}, err
	}
	return DiscoverResult{CommandID: commandID, InventoryBytes: len(final.GetDiscover().GetInventoryJson())}, nil
}

func (a *Activities) dispatch(ctx context.Context, agentID string, cmd *agentv1.Command) (*agentv1.CommandUpdate, error) {
	d := agentcmd.Dispatcher{Control: a.Control, Token: a.Token, WaitForAgent: a.WaitForAgent}
	return d.Dispatch(ctx, agentID, cmd, nil)
}
