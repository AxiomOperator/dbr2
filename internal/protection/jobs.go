// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Job is one backup or restore execution (the Jobs view).
type Job struct {
	ID              string     `json:"id" doc:"Recovery point ID (backups) or restore ID (restores)."`
	Type            string     `json:"type" enum:"backup,restore"`
	ApplicationID   string     `json:"application_id"`
	ApplicationName string     `json:"application_name"`
	Hostname        string     `json:"hostname"`
	State           string     `json:"state" enum:"running,succeeded,partial,failed,rolled_back,missing"`
	Detail          string     `json:"detail" doc:"Consistency mode (backups) or current step / mode (restores)."`
	Trigger         string     `json:"trigger,omitempty"`
	RequestedBy     string     `json:"requested_by,omitempty"`
	Error           string     `json:"error,omitempty"`
	SizeBytes       int64      `json:"size_bytes,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	ApplicationID *uuid.UUID
	Type          string // backup | restore | ""
	State         string
	Limit         int32
}

var backupStates = map[string]string{"pending": "running", "committed": "succeeded", "failed": "failed", "missing": "missing", "deleting": "running"}

// ListJobs merges backups (recovery point index, including failures) and
// restores, newest first.
func (s *Service) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var jobs []Job
	if f.Type != "restore" {
		rps, err := s.ListRecoveryPoints(ctx, RecoveryPointFilter{ApplicationID: f.ApplicationID, Limit: f.Limit})
		if err != nil {
			return nil, err
		}
		for _, r := range rps {
			j := Job{ID: r.ID, Type: "backup", ApplicationID: r.ApplicationID.String(), ApplicationName: r.ApplicationName,
				Hostname: r.Hostname, State: backupStates[r.State], Detail: r.ConsistencyMode, Trigger: r.Trigger,
				SizeBytes: r.SizeBytes, StartedAt: r.CreatedAt, FinishedAt: r.CommittedAt}
			if r.State == "committed" && r.Status != nil && *r.Status == "partial" {
				j.State = "partial"
			}
			if r.State == "failed" {
				t := r.UpdatedAt
				j.FinishedAt = &t
			}
			if r.Error != nil {
				j.Error = *r.Error
			}
			jobs = append(jobs, j)
		}
	}
	if f.Type != "backup" {
		rs, err := s.ListRestores(ctx, f.ApplicationID, nil, f.Limit)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			state := r.State
			if state == "requested" {
				state = "running"
			}
			detail := r.Mode
			if r.Step != nil && (r.State == "running" || r.State == "requested") {
				detail = *r.Step
			}
			j := Job{ID: r.ID, Type: "restore", ApplicationID: r.SourceApplicationID.String(), ApplicationName: r.ApplicationName,
				Hostname: r.TargetHostname, State: state, Detail: detail, Trigger: "manual", RequestedBy: r.RequestedByDisplay,
				StartedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
			if r.Error != nil {
				j.Error = *r.Error
			}
			jobs = append(jobs, j)
		}
	}
	out := jobs[:0]
	for _, j := range jobs {
		if f.State == "" || j.State == f.State {
			out = append(out, j)
		}
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].StartedAt.After(out[k].StartedAt) })
	if int32(len(out)) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}
