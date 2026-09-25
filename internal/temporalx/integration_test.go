// SPDX-License-Identifier: Apache-2.0

//go:build integration

package temporalx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/workflows"
	"github.com/AxiomOperator/dbr2/workflows/diag"
	"github.com/AxiomOperator/dbr2/workflows/ops"
)

// TestExclusivityAndTriggerAgainstRealServer runs against a Temporal dev
// server (downloaded by the SDK test suite) to prove the ADR-0011 behaviour
// end to end, not just in the in-memory test environment.
func TestExclusivityAndTriggerAgainstRealServer(t *testing.T) {
	ctx := context.Background()
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{ClientOptions: &client.Options{Namespace: "default"}})
	if err != nil {
		t.Skipf("temporal dev server unavailable: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	c := srv.Client()
	const q = "dbr2-test"
	w := worker.New(c, q, worker.Options{})
	workflows.Register(w, &diag.Activities{}, nil)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)

	// Hold the application with a long-running operation.
	run, err := temporalx.StartApplicationOperation(ctx, c, q, "app-7", diag.ApplicationSelfTest, diag.SelfTestInput{ApplicationID: "app-7", Hold: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	// A second operation — even of another type — is rejected, not silently attached.
	_, err = temporalx.StartApplicationOperation(ctx, c, q, "app-7", ops.ScheduledOperationTrigger, ops.TriggerInput{})
	var busy *temporalx.ErrOperationInProgress
	if !errors.As(err, &busy) || busy.RunningRunID != run.GetRunID() {
		t.Fatalf("expected ErrOperationInProgress for run %s, got %v", run.GetRunID(), err)
	}

	// A scheduled trigger records a skip while the application is busy.
	trig, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "schedule-app-7-1", TaskQueue: q}, ops.ScheduledOperationTrigger,
		ops.TriggerInput{ApplicationID: "app-7", WorkflowType: "ApplicationSelfTest", Args: []any{diag.SelfTestInput{ApplicationID: "app-7"}}})
	if err != nil {
		t.Fatal(err)
	}
	var res ops.TriggerResult
	if err := trig.Get(ctx, &res); err != nil || res.Outcome != ops.OutcomeSkippedOverlap {
		t.Fatalf("trigger while busy: %+v %v", res, err)
	}

	// Cancel (never terminate) releases the application; compensation ran.
	if err := c.CancelWorkflow(ctx, run.GetID(), run.GetRunID()); err != nil {
		t.Fatal(err)
	}
	_ = run.Get(ctx, nil)

	// Now the scheduled trigger starts the operation.
	trig2, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "schedule-app-7-2", TaskQueue: q}, ops.ScheduledOperationTrigger,
		ops.TriggerInput{ApplicationID: "app-7", WorkflowType: "ApplicationSelfTest", Args: []any{diag.SelfTestInput{ApplicationID: "app-7"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := trig2.Get(ctx, &res); err != nil || res.Outcome != ops.OutcomeStarted || res.WorkflowID != "application/app-7" {
		t.Fatalf("trigger when free: %+v %v", res, err)
	}
}
