// SPDX-License-Identifier: Apache-2.0

// Package diag contains diagnostic workflows used to validate the Temporal
// wiring (exclusivity, cancellation, compensation) before real application
// operations exist. They never touch hosts.
package diag

import (
	"context"
	"errors"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/AxiomOperator/dbr2/workflows/saga"
)

// SelfTestInput configures ApplicationSelfTest.
type SelfTestInput struct {
	ApplicationID string
	// Hold keeps the workflow running (to test exclusivity / cancellation).
	Hold time.Duration
	// FailAfterClaim makes the protected step fail permanently.
	FailAfterClaim bool
}

// SelfTestResult reports which steps ran.
type SelfTestResult struct {
	Claimed     bool
	Compensated bool
}

// Activities used by the self-test.
type Activities struct {
	// OnCompensate is called when the compensation activity runs (tests).
	OnCompensate func(applicationID string)
}

// Claim simulates quiescing an application.
func (a *Activities) Claim(ctx context.Context, applicationID string) error {
	activity.GetLogger(ctx).Info("self-test claim", "application_id", applicationID)
	return nil
}

// Protect simulates the protected step.
func (a *Activities) Protect(ctx context.Context, fail bool) error {
	if fail {
		return temporal.NewNonRetryableApplicationError("self-test failure", "SelfTest", errors.New("requested failure"))
	}
	return nil
}

// Release simulates resuming the application (the compensation).
func (a *Activities) Release(ctx context.Context, applicationID string) error {
	if a.OnCompensate != nil {
		a.OnCompensate(applicationID)
	}
	return nil
}

// ApplicationSelfTest is a diagnostic application operation: claim →
// (hold) → protect, with Release registered as saga compensation.
func ApplicationSelfTest(ctx workflow.Context, in SelfTestInput) (res SelfTestResult, err error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var a *Activities
	var sg saga.Saga
	defer func() {
		if cerr := sg.Run(ctx); cerr != nil {
			err = errors.Join(err, cerr)
		} else if res.Claimed {
			res.Compensated = true
		}
	}()
	if err = workflow.ExecuteActivity(ctx, a.Claim, in.ApplicationID).Get(ctx, nil); err != nil {
		return res, err
	}
	res.Claimed = true
	sg.Always("release", func(c workflow.Context) error {
		return workflow.ExecuteActivity(c, a.Release, in.ApplicationID).Get(c, nil)
	})
	if in.Hold > 0 {
		if err = workflow.Sleep(ctx, in.Hold); err != nil {
			return res, err // cancelled: compensation still runs
		}
	}
	err = workflow.ExecuteActivity(ctx, a.Protect, in.FailAfterClaim).Get(ctx, nil)
	return res, err
}
