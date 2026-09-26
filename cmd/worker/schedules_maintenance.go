// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// ensureMaintenanceSchedules creates the retention (daily) and
// verification (weekly) schedules once; existing ones are left alone.
func ensureMaintenanceSchedules(ctx context.Context, c client.Client, cfg *config.Worker, log *slog.Logger) {
	type sched struct {
		id, cron, workflowID string
		wf                   any
		args                 []any
	}
	all := []sched{
		{"platform-retention", cfg.RetentionCron, backup.RetentionWorkflowID, backup.RetentionWorkflow, nil},
		{"platform-verify", cfg.VerifyCron, backup.VerifyAllWorkflowID, backup.VerifyAllWorkflow, []any{cfg.VerifyReadPercent}},
	}
	for _, s := range all {
		for {
			_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
				ID: s.id, Spec: client.ScheduleSpec{CronExpressions: []string{s.cron}}, Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				Action: &client.ScheduleWorkflowAction{ID: s.workflowID, Workflow: s.wf, Args: s.args, TaskQueue: cfg.Temporal.TaskQueue},
			})
			if err == nil || errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
				if err == nil {
					log.Info("created schedule", "schedule_id", s.id, "cron", s.cron)
				}
				break
			}
			log.Warn("creating a maintenance schedule failed; retrying", "schedule_id", s.id, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}
}
