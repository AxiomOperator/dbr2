// SPDX-License-Identifier: Apache-2.0

// Package saga provides guaranteed compensation for Temporal workflows
// (ADR-0005 Layer 1). Compensations registered after a step succeeds run in
// reverse order in a disconnected context, so they execute when the workflow
// fails AND when it is cancelled. They do NOT run on termination or
// workflow-level timeout (spike-verified), which is why DBR² offers Cancel
// only and relies on the agent dead-man switch as Layer 2.
package saga

import (
	"errors"

	"go.temporal.io/sdk/workflow"
)

// Saga accumulates compensations.
type Saga struct {
	steps []step
}

type step struct {
	name string
	fn   func(ctx workflow.Context) error
}

// Add registers a compensation to run if the workflow does not complete.
func (s *Saga) Add(name string, fn func(ctx workflow.Context) error) {
	s.steps = append(s.steps, step{name: name, fn: fn})
}

// Always registers a compensation that must run on success too (e.g.
// "resume the application" after a successful backup).
func (s *Saga) Always(name string, fn func(ctx workflow.Context) error) {
	s.Add(name, fn)
}

// Run executes every registered compensation in reverse order in a
// disconnected context and returns the joined errors. Call it from a defer:
//
//	var sg saga.Saga
//	defer func() { err = errors.Join(err, sg.Run(ctx)) }()
func (s *Saga) Run(ctx workflow.Context) error {
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	var errs []error
	for i := len(s.steps) - 1; i >= 0; i-- {
		st := s.steps[i]
		if err := st.fn(dctx); err != nil {
			workflow.GetLogger(dctx).Error("compensation failed", "step", st.name, "error", err)
			errs = append(errs, err)
		}
	}
	s.steps = nil
	return errors.Join(errs...)
}
