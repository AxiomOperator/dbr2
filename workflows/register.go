// SPDX-License-Identifier: Apache-2.0

// Package workflows registers every DBR² workflow and activity on a worker.
package workflows

import (
	"go.temporal.io/sdk/worker"

	"github.com/AxiomOperator/dbr2/workflows/backup"
	"github.com/AxiomOperator/dbr2/workflows/diag"
	"github.com/AxiomOperator/dbr2/workflows/hosts"
	"github.com/AxiomOperator/dbr2/workflows/ops"
	"github.com/AxiomOperator/dbr2/workflows/platform"
	"github.com/AxiomOperator/dbr2/workflows/restore"
)

// Register adds all workflows and activities to w.
func Register(w worker.Registry, diagActs *diag.Activities, hostActs *hosts.Activities, backupActs *backup.Activities, restoreActs *restore.Activities, platformActs *platform.Activities) {
	w.RegisterWorkflow(ops.ScheduledOperationTrigger)
	w.RegisterWorkflow(diag.ApplicationSelfTest)
	w.RegisterActivity(diagActs)
	w.RegisterWorkflow(hosts.DiscoverHost)
	if hostActs != nil {
		w.RegisterActivity(hostActs)
	}
	w.RegisterWorkflow(backup.BackupWorkflow)
	w.RegisterWorkflow(backup.OrphanGC)
	w.RegisterWorkflow(backup.RetentionWorkflow)
	w.RegisterWorkflow(backup.VerifyRepositoryWorkflow)
	w.RegisterWorkflow(backup.Reindex)
	if backupActs != nil {
		w.RegisterActivity(backupActs)
	}
	w.RegisterWorkflow(restore.RestoreWorkflow)
	if restoreActs != nil {
		w.RegisterActivity(restoreActs)
	}
	w.RegisterWorkflow(platform.PlatformProtectionWorkflow)
	if platformActs != nil {
		w.RegisterActivity(platformActs)
	}
}
