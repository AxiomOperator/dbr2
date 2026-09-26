// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// finalizeRestore commits or rolls back a restore from its journal
// (idempotent; works after an agent restart).
func (a *Agent) finalizeRestore(ctx context.Context, c *agentv1.FinalizeRestoreCommand) (*agentv1.FinalizeRestoreResult, error) {
	if err := validRestoreID(c.RestoreId); err != nil {
		return nil, err
	}
	unlock := a.restores.lock(c.RestoreId)
	defer unlock()
	j, err := a.restores.load(c.RestoreId)
	if err != nil {
		return nil, err
	}
	if j == nil {
		now := time.Now().UTC()
		j = &restoreJournal{RestoreID: c.RestoreId, State: restoreActive, CreatedAt: now, Swaps: []*swapRecord{}}
	}
	want := map[agentv1.FinalizeAction]string{agentv1.FinalizeAction_FINALIZE_ACTION_COMMIT: restoreCommitted,
		agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK: restoreRolledBack}[c.Action]
	switch {
	case want == "":
		return nil, permanent(fmt.Errorf("unsupported finalize action %s", c.Action))
	case j.State == want:
		return &agentv1.FinalizeRestoreResult{AlreadyFinalized: true}, nil
	case j.State != restoreActive:
		return nil, permanent(fmt.Errorf("restore %s is already %s", c.RestoreId, j.State))
	}
	if want == restoreCommitted {
		return a.commitRestore(j)
	}
	return a.rollbackRestore(ctx, j, c.RestartContainerIds)
}

// commitRestore deletes the previous content kept by the swaps.
func (a *Agent) commitRestore(j *restoreJournal) (*agentv1.FinalizeRestoreResult, error) {
	for _, r := range j.Swaps {
		if r.State == swapStaging || r.State == swapSwapping {
			return nil, permanent(fmt.Errorf("restore %s has an interrupted swap of %s; roll it back instead", j.RestoreID, r.Target))
		}
	}
	res := &agentv1.FinalizeRestoreResult{}
	var errs []error
	for _, r := range j.Swaps {
		if r.State != swapSwapped {
			continue
		}
		if err := os.RemoveAll(r.Previous); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", r.Previous, err))
			continue
		}
		_ = os.RemoveAll(r.Staging)
		_ = os.RemoveAll(r.Discard)
		r.State = swapCommitted
		res.PathsCommitted++
	}
	if len(errs) == 0 {
		j.State = restoreCommitted
	}
	if err := a.restores.save(j); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...) // retryable: a repeat continues
	}
	a.log.Info("restore committed", "restore_id", j.RestoreID, "paths", res.PathsCommitted)
	return res, nil
}

// rollbackRestore stops the application, swaps the previous content back,
// removes what the restore created and starts the containers that were
// running before. A failed step leaves the journal active; a repeat
// continues where it stopped.
func (a *Agent) rollbackRestore(ctx context.Context, j *restoreJournal, restart []string) (*agentv1.FinalizeRestoreResult, error) {
	res := &agentv1.FinalizeRestoreResult{}
	needCtl := len(restart) > 0 || len(j.CreatedContainers) > 0 || len(j.CreatedNetworks) > 0 || len(j.CreatedVolumes) > 0
	var ctl runtime.RestoreControl
	if needCtl {
		var err error
		if ctl, err = a.restoreControl(); err != nil {
			return nil, err
		}
	}
	var errs []error
	// 1. Stop everything that could hold the restored data open.
	var stop []string
	for _, c := range j.CreatedContainers {
		stop = append(stop, j.ref(c))
	}
	stop = append(stop, restart...)
	for _, id := range stop {
		d, err := ctl.InspectDetails(ctx, id)
		if errors.Is(err, runtime.ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s: %w", id, err))
			continue
		}
		if d.State == "paused" {
			if err := ctl.Unpause(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("unpause %s: %w", id, err))
				continue
			}
		}
		if d.Running {
			if err := ctl.Stop(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("stop %s: %w", id, err))
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...) // never swap under a running application
	}
	// 2. Swap the previous content back, newest first.
	for i := len(j.Swaps) - 1; i >= 0; i-- {
		r := j.Swaps[i]
		changed, err := undoSwap(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("roll back %s: %w", r.Target, err))
			continue
		}
		if changed {
			res.PathsRolledBack++
		}
	}
	if err := a.restores.save(j); err != nil {
		errs = append(errs, err)
	}
	// 3. Remove what the restore created.
	var keep []createdContainer
	for _, c := range j.CreatedContainers {
		err := ctl.RemoveContainer(ctx, j.ref(c))
		if err != nil && !errors.Is(err, runtime.ErrNotFound) {
			errs = append(errs, fmt.Errorf("remove container %s: %w", c.Name, err))
			keep = append(keep, c)
			continue
		}
		res.RemovedContainers = append(res.RemovedContainers, c.Name)
	}
	j.CreatedContainers = keep
	var keepNets []string
	for _, n := range slices.Backward(j.CreatedNetworks) {
		if err := ctl.RemoveNetwork(ctx, n); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			errs = append(errs, fmt.Errorf("remove network %s: %w", n, err))
			keepNets = append(keepNets, n)
		}
	}
	j.CreatedNetworks = keepNets
	var keepVols []string
	for _, v := range j.CreatedVolumes {
		if err := ctl.RemoveVolume(ctx, v); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			errs = append(errs, fmt.Errorf("remove volume %s: %w", v, err))
			keepVols = append(keepVols, v)
		}
	}
	j.CreatedVolumes = keepVols
	if err := a.restores.save(j); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	// 4. Bring the previous application back.
	for _, id := range restart {
		d, err := ctl.InspectDetails(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s: %w", id, err))
			continue
		}
		if !d.Running {
			if err := ctl.Start(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("start %s: %w", id, err))
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	j.State = restoreRolledBack
	if err := a.restores.save(j); err != nil {
		return nil, err
	}
	a.log.Info("restore rolled back", "restore_id", j.RestoreID, "paths", res.PathsRolledBack,
		"removed_containers", len(res.RemovedContainers), "restarted", len(restart))
	return res, nil
}
