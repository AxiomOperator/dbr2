// SPDX-License-Identifier: Apache-2.0

// Package temporalx holds DBR²'s Temporal conventions (ADR-0009, ADR-0011).
//
// StartApplicationOperation is the ONLY sanctioned way to start a workflow
// that operates on a live application: it applies the exclusivity policies
// and — crucially — WorkflowExecutionErrorWhenAlreadyStarted, without which
// the Go SDK silently returns a handle to the already-running execution
// (spike finding, spikes/temporal/RESULTS.md). A lint test in this package
// forbids ExecuteWorkflow for application workflows elsewhere and forbids
// TerminateWorkflow everywhere (termination skips saga compensation, ADR-0005).
package temporalx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
)

// ApplicationIDPrefix is the workflow-ID namespace for live-application
// operations (backup, restore, migration).
const ApplicationIDPrefix = "application/"

// ApplicationWorkflowID returns the exclusive workflow ID for an application.
func ApplicationWorkflowID(applicationID string) string {
	return ApplicationIDPrefix + applicationID
}

// RepositoryWorkflowID returns the workflow ID for a repository-wide operation.
func RepositoryWorkflowID(repositoryID, operation string) string {
	return "repository/" + repositoryID + "/" + operation
}

// ErrOperationInProgress means another operation already holds the
// application (ADR-0011). RunningRunID identifies it.
type ErrOperationInProgress struct {
	WorkflowID   string
	RunningRunID string
}

func (e *ErrOperationInProgress) Error() string {
	return fmt.Sprintf("operation already in progress for %s (run %s)", e.WorkflowID, e.RunningRunID)
}

// ApplicationStartOptions returns the start options every application
// operation must use.
func ApplicationStartOptions(applicationID, taskQueue string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                                       ApplicationWorkflowID(applicationID),
		TaskQueue:                                taskQueue,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
		// No WorkflowRunTimeout / WorkflowExecutionTimeout: workflow-level
		// timeouts skip compensation (ADR-0005).
	}
}

// StartApplicationOperation starts workflow `wf` for an application. It
// returns *ErrOperationInProgress when another operation holds the ID.
func StartApplicationOperation(ctx context.Context, c client.Client, taskQueue, applicationID string, wf any, args ...any) (client.WorkflowRun, error) {
	opts := ApplicationStartOptions(applicationID, taskQueue)
	run, err := c.ExecuteWorkflow(ctx, opts, wf, args...)
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &started) {
		return nil, &ErrOperationInProgress{WorkflowID: opts.ID, RunningRunID: started.RunId}
	}
	return run, err
}

// IsApplicationWorkflowID reports whether id is in the application namespace.
func IsApplicationWorkflowID(id string) bool { return strings.HasPrefix(id, ApplicationIDPrefix) }

// Dial connects a Temporal client lazily (no network I/O until first use).
func Dial(address, namespace string, log *slog.Logger) (client.Client, error) {
	return client.NewLazyClient(client.Options{
		HostPort: address, Namespace: namespace,
		Logger: tlog.NewStructuredLogger(log.With("component", "temporal")),
	})
}

// NonRetryable wraps err so Temporal does not retry the activity.
func NonRetryable(err error, kind string) error {
	return temporal.NewNonRetryableApplicationError(err.Error(), kind, err)
}
