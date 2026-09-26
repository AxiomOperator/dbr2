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
	platformwf "github.com/AxiomOperator/dbr2/workflows/platform"
)

// orphanGCScheduleID names the platform maintenance schedule.
const orphanGCScheduleID = "platform-orphan-gc"

// ensureSchedules creates the platform maintenance schedules once (retrying
// until Temporal is reachable). Existing schedules are left as they are.
func ensureSchedules(ctx context.Context, c client.Client, cfg *config.Worker, log *slog.Logger) {
	for {
		_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
			ID:      orphanGCScheduleID,
			Spec:    client.ScheduleSpec{CronExpressions: []string{cfg.OrphanGCCron}},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			Action: &client.ScheduleWorkflowAction{ID: backup.OrphanGCWorkflowID, Workflow: backup.OrphanGC,
				Args: []any{cfg.OrphanGrace}, TaskQueue: cfg.Temporal.TaskQueue},
		})
		if err == nil || errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
			if err == nil {
				log.Info("created schedule", "schedule_id", orphanGCScheduleID, "cron", cfg.OrphanGCCron)
			}
			return
		}
		log.Warn("creating the orphan GC schedule failed; retrying", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// platformScheduleID names the daily Platform Protection schedule (ADR-0008).
const platformScheduleID = "platform-protection"

// ensurePlatformSchedule creates the Platform Protection schedule once
// (retrying until Temporal is reachable). An existing schedule is kept.
func ensurePlatformSchedule(ctx context.Context, c client.Client, cfg *config.Worker, log *slog.Logger) {
	for {
		_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
			ID:      platformScheduleID,
			Spec:    client.ScheduleSpec{CronExpressions: []string{cfg.PlatformBackupCron}},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			Action: &client.ScheduleWorkflowAction{ID: platformwf.WorkflowID, Workflow: platformwf.PlatformProtectionWorkflow,
				Args: []any{platformwf.Input{Trigger: "scheduled"}}, TaskQueue: cfg.Temporal.TaskQueue},
		})
		if err == nil || errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
			if err == nil {
				log.Info("created schedule", "schedule_id", platformScheduleID, "cron", cfg.PlatformBackupCron)
			}
			return
		}
		log.Warn("creating the platform protection schedule failed; retrying", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}
