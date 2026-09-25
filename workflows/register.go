// SPDX-License-Identifier: Apache-2.0

// Package workflows registers every DBR² workflow and activity on a worker.
package workflows

import (
	"go.temporal.io/sdk/worker"

	"github.com/AxiomOperator/dbr2/workflows/diag"
	"github.com/AxiomOperator/dbr2/workflows/ops"
)

// Register adds all workflows and activities to w.
func Register(w worker.Registry, diagActs *diag.Activities) {
	w.RegisterWorkflow(ops.ScheduledOperationTrigger)
	w.RegisterWorkflow(diag.ApplicationSelfTest)
	w.RegisterActivity(diagActs)
}
