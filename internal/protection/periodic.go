// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"time"
)

// RunPeriodic runs the protection housekeeping loop until ctx ends:
// schedule sync (10 min), contract evaluation (5 min), escrow health and
// Repository retirement (hourly). Every task is idempotent, so running it
// on several dbr2-server instances is harmless.
func (s *Service) RunPeriodic(ctx context.Context) {
	type task struct {
		name  string
		every time.Duration
		run   func(context.Context) error
		next  time.Time
	}
	tasks := []*task{
		{name: "sync-schedules", every: 10 * time.Minute, run: s.SyncSchedules},
		{name: "evaluate-contracts", every: 5 * time.Minute, run: s.EvaluateContracts},
		{name: "escrow-health", every: time.Hour, run: s.CheckEscrowAndAlert},
		{name: "retire-repositories", every: time.Hour, run: s.RetireDueRepositories},
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		now := time.Now()
		for _, tk := range tasks {
			if now.Before(tk.next) {
				continue
			}
			tk.next = now.Add(tk.every)
			tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := tk.run(tctx); err != nil && ctx.Err() == nil && s.opts.Log != nil {
				s.opts.Log.Warn("protection housekeeping failed", "task", tk.name, "err", err)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
