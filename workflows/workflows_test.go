// SPDX-License-Identifier: Apache-2.0

package workflows_test

import (
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/AxiomOperator/dbr2/workflows/diag"
)

func run(t *testing.T, in diag.SelfTestInput, beforeRun func(env *testsuite.TestWorkflowEnvironment)) (*testsuite.TestWorkflowEnvironment, *int) {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	released := 0
	env.RegisterActivity(&diag.Activities{OnCompensate: func(string) { released++ }})
	if beforeRun != nil {
		beforeRun(env)
	}
	env.ExecuteWorkflow(diag.ApplicationSelfTest, in)
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	return env, &released
}

func TestCompensationRunsOnSuccess(t *testing.T) {
	env, released := run(t, diag.SelfTestInput{ApplicationID: "app-1"}, nil)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var res diag.SelfTestResult
	_ = env.GetWorkflowResult(&res)
	if !res.Claimed || !res.Compensated || *released != 1 {
		t.Fatalf("result %+v released=%d", res, *released)
	}
}

func TestCompensationRunsOnFailure(t *testing.T) {
	env, released := run(t, diag.SelfTestInput{ApplicationID: "app-1", FailAfterClaim: true}, nil)
	var appErr *temporal.ApplicationError
	if err := env.GetWorkflowError(); !errors.As(err, &appErr) {
		t.Fatalf("expected application error, got %v", err)
	}
	if *released != 1 {
		t.Fatalf("release ran %d times after failure", *released)
	}
}

func TestCompensationRunsOnCancel(t *testing.T) {
	env, released := run(t, diag.SelfTestInput{ApplicationID: "app-1", Hold: time.Hour}, func(env *testsuite.TestWorkflowEnvironment) {
		env.RegisterDelayedCallback(env.CancelWorkflow, time.Minute)
	})
	if err := env.GetWorkflowError(); !temporal.IsCanceledError(err) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if *released != 1 {
		t.Fatalf("release ran %d times after cancel", *released)
	}
}
