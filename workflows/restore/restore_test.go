// SPDX-License-Identifier: Apache-2.0

package restore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

type rec struct {
	failData bool
	mu       sync.Mutex
	order    []string
	states   []string
}

func (r *rec) add(s string) { r.mu.Lock(); r.order = append(r.order, s); r.mu.Unlock() }

func plan(cross bool) *controlv1.PrepareRestoreResponse {
	p := &controlv1.PrepareRestoreResponse{RestoreId: "rs_1", Repository: &controlv1.Repository{Id: "repo"}, SourceAgentId: "a1", TargetAgentId: "a1",
		Components:       []*agentv1.RestoreSpec{{Name: "volume:data"}},
		ConfigSnapshotId: "cfg", RecreateContainers: true, StopContainerIds: []string{"c1"}, LeaseSeconds: 3600,
		Images: []*agentv1.ImageSpec{{Ref: "x", Digest: "sha256:1"}}}
	if cross {
		p.CrossHost, p.TargetAgentId, p.StopContainerIds = true, "a2", nil
	}
	return p
}

func env(t *testing.T, p *controlv1.PrepareRestoreResponse, healthy bool, r *rec) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	e := s.NewTestWorkflowEnvironment()
	var a *Activities
	var ba *backup.Activities
	e.RegisterActivity(a)
	e.RegisterActivity(ba)
	e.OnActivity(a.PrepareRestore, mock.Anything, mock.Anything).Return(p, nil)
	e.OnActivity(a.UpdateRestore, mock.Anything, mock.Anything).Return(func(_ context.Context, u *controlv1.UpdateRestoreRequest) error {
		if u.State != "running" {
			r.mu.Lock()
			r.states = append(r.states, u.State)
			r.mu.Unlock()
		}
		return nil
	})
	e.OnActivity(a.GrantRestoreAccess, mock.Anything, mock.Anything).Return(func(context.Context, string) (string, error) { r.add("grant"); return "g1", nil })
	e.OnActivity(a.RevokeRestoreAccess, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, string, string) error { r.add("revoke"); return nil })
	e.OnActivity(ba.EnsureAgentAccess, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	e.OnActivity(a.EnsureImages, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, string, []*agentv1.ImageSpec) ([]*agentv1.ImageResult, error) {
		r.add("images")
		return nil, nil
	})
	e.OnActivity(ba.Quiesce, mock.Anything, mock.Anything).Return(func(_ context.Context, in backup.QuiesceInput) (*agentv1.QuiesceResult, error) {
		r.add("stop")
		if in.Mode != agentv1.QuiesceMode_QUIESCE_MODE_STOP || in.LeaseID != "rs_1" {
			t.Errorf("quiesce %+v", in)
		}
		return &agentv1.QuiesceResult{PreState: []*agentv1.ContainerState{{Id: "c1", State: "running"}}}, nil
	})
	e.OnActivity(ba.Resume, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, string, string) (*agentv1.ResumeResult, error) {
		r.add("resume")
		return &agentv1.ResumeResult{}, nil
	})
	e.OnActivity(a.RestoreComponents, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ string, c *agentv1.RestoreComponentsCommand) ([]*agentv1.RestoreComponentResult, error) {
		r.add("data")
		if r.failData {
			return nil, temporal.NewNonRetryableApplicationError("verify failed", "AgentCommandFailed", nil)
		}
		if c.FreshSession != p.CrossHost {
			t.Errorf("fresh_session = %v", c.FreshSession)
		}
		return []*agentv1.RestoreComponentResult{{Name: "volume:data", Status: "restored"}}, nil
	})
	e.OnActivity(a.RecreateContainers, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, string, *agentv1.RecreateContainersCommand) (*agentv1.RecreateContainersResult, error) {
		r.add("recreate")
		return &agentv1.RecreateContainersResult{Containers: []*agentv1.RecreatedContainer{{Name: "web", Status: "created", ContainerId: "n1", WasRunning: true}}}, nil
	})
	e.OnActivity(a.StartContainers, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, string, string, []string) error {
		r.add("start")
		return nil
	})
	e.OnActivity(a.CheckHealth, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ string, ids []string, _ uint32) (*agentv1.CheckHealthResult, error) {
		r.add("health:" + strings.Join(ids, "+"))
		return &agentv1.CheckHealthResult{Ok: healthy, Containers: []*agentv1.ContainerHealth{{Name: "web", Ok: healthy, State: "exited"}}}, nil
	})
	e.OnActivity(a.FinalizeRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _, _ string, act agentv1.FinalizeAction, restart []string) error {
		r.add(strings.ToLower(strings.TrimPrefix(act.String(), "FINALIZE_ACTION_")) + ":" + strings.Join(restart, "+"))
		return nil
	})
	return e
}

func TestInPlaceRestoreOrder(t *testing.T) {
	r := &rec{}
	e := env(t, plan(false), true, r)
	e.ExecuteWorkflow(RestoreWorkflow, Input{RestoreID: "rs_1"})
	if err := e.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	want := "images stop data recreate resume start health:c1+n1 commit:"
	if got := strings.Join(r.order, " "); got != want {
		t.Fatalf("order = %s\nwant    %s", got, want)
	}
	if strings.Join(r.states, ",") != "succeeded" {
		t.Fatalf("states %v", r.states)
	}
}

func TestUnhealthyRollsBack(t *testing.T) {
	r := &rec{}
	e := env(t, plan(false), false, r)
	e.ExecuteWorkflow(RestoreWorkflow, Input{RestoreID: "rs_1"})
	var ae *temporal.ApplicationError
	if err := e.GetWorkflowError(); !errors.As(err, &ae) || ae.Type() != ErrUnhealthy {
		t.Fatalf("err = %v", err)
	}
	want := "images stop data recreate resume start health:c1+n1 rollback:c1"
	if got := strings.Join(r.order, " "); got != want {
		t.Fatalf("order = %s\nwant    %s", got, want)
	}
	if strings.Join(r.states, ",") != "rolled_back" {
		t.Fatalf("states %v", r.states)
	}
}

func TestDataFailureRollsBackAndResumes(t *testing.T) {
	r := &rec{failData: true}
	e := env(t, plan(false), true, r)
	e.ExecuteWorkflow(RestoreWorkflow, Input{RestoreID: "rs_1"})
	if e.GetWorkflowError() == nil {
		t.Fatal("expected failure")
	}
	if got := strings.Join(r.order, " "); got != "images stop data rollback:c1 resume" {
		t.Fatalf("order = %s", got)
	}
}

func TestCrossHostGrantsAndRevokes(t *testing.T) {
	r := &rec{}
	e := env(t, plan(true), true, r)
	e.ExecuteWorkflow(RestoreWorkflow, Input{RestoreID: "rs_1"})
	if err := e.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.order, " ")
	if !strings.HasPrefix(got, "grant images data recreate start health:n1 commit:") || !strings.HasSuffix(got, "revoke") {
		t.Fatalf("order = %s", got)
	}
}
