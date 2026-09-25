package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

var (
	c        client.Client
	failures int
)

func check(ok bool, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if ok {
		fmt.Println("  PASS", msg)
	} else {
		failures++
		fmt.Println("  FAIL", msg)
	}
}

func section(s string) { fmt.Printf("\n=== %s\n", s) }

// ---------- worker subprocess control ----------

type workerProc struct{ cmd *exec.Cmd }

func startWorker() *workerProc {
	exe, _ := os.Executable()
	logf, _ := os.OpenFile(filepath.Join(filepath.Dir(exe), "worker.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	cmd := exec.Command(exe, "worker")
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	fmt.Printf("  [worker started pid=%d]\n", cmd.Process.Pid)
	return &workerProc{cmd}
}

func (w *workerProc) kill() {
	_ = w.cmd.Process.Signal(syscall.SIGKILL)
	_ = w.cmd.Wait()
	fmt.Printf("  [worker pid=%d SIGKILLed]\n", w.cmd.Process.Pid)
}

func (w *workerProc) stop() {
	_ = w.cmd.Process.Signal(syscall.SIGTERM)
	_ = w.cmd.Wait()
}

// ---------- helpers ----------

func appOpts(id string, errWhenStarted bool) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                                       id,
		TaskQueue:                                TaskQueue,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowExecutionErrorWhenAlreadyStarted: errWhenStarted,
	}
}

// activityTrail returns ordered "EVENT:ActivityType" entries from history.
func activityTrail(ctx context.Context, wfID, runID string) []string {
	types := map[int64]string{}
	var out []string
	it := c.GetWorkflowHistory(ctx, wfID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			panic(err)
		}
		switch e.EventType {
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			t := e.GetActivityTaskScheduledEventAttributes().ActivityType.Name
			types[e.EventId] = t
			out = append(out, "scheduled:"+t)
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			out = append(out, "completed:"+types[e.GetActivityTaskCompletedEventAttributes().ScheduledEventId])
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			a := e.GetActivityTaskFailedEventAttributes()
			out = append(out, "failed:"+types[a.ScheduledEventId]+"("+a.Failure.GetMessage()+")")
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			a := e.GetActivityTaskTimedOutEventAttributes()
			out = append(out, "timedout:"+types[a.ScheduledEventId]+"("+a.Failure.GetMessage()+")")
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
			out = append(out, "canceled:"+types[e.GetActivityTaskCanceledEventAttributes().ScheduledEventId])
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCEL_REQUESTED:
			out = append(out, "WF_CANCEL_REQUESTED")
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
			out = append(out, "WF_TERMINATED")
		}
	}
	return out
}

func count(trail []string, entry string) int {
	n := 0
	for _, t := range trail {
		if t == entry {
			n++
		}
	}
	return n
}

func indexOf(trail []string, prefix string) int {
	for i, t := range trail {
		if strings.HasPrefix(t, prefix) {
			return i
		}
	}
	return -1
}

func status(ctx context.Context, wfID, runID string) enumspb.WorkflowExecutionStatus {
	d, err := c.DescribeWorkflowExecution(ctx, wfID, runID)
	if err != nil {
		panic(err)
	}
	return d.WorkflowExecutionInfo.Status
}

func waitFor(ctx context.Context, wfID, runID, entry string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if count(activityTrail(ctx, wfID, runID), entry) > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	panic("timeout waiting for " + entry + " in " + wfID)
}

func cleanup(ctx context.Context) {
	for _, id := range []string{"application/app-1", "application/app-q3-ok", "application/app-q3-fail",
		"application/app-q3-cancel", "application/app-q3-kill", "application/app-q3-term",
		"application/app-q3-stall", "application/app-q3-stall-retry"} {
		_ = c.TerminateWorkflow(ctx, id, "", "spike cleanup")
	}
	for _, m := range []string{"child", "activity"} {
		_ = c.ScheduleClient().GetHandle(ctx, "backup/app-1/"+m).Delete(ctx)
	}
}

// ---------- scenarios ----------

func runScenarios(only []string) error {
	var err error
	c, err = dial(slog.LevelWarn)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx := context.Background()
	cleanup(ctx)

	w := startWorker()
	defer func() { w.stop() }()

	want := func(name string) bool {
		if len(only) == 0 {
			return true
		}
		for _, o := range only {
			if strings.HasPrefix(name, o) {
				return true
			}
		}
		return false
	}

	if want("q2") {
		q2abc(ctx)
		q2d(ctx, "child")
		q2d(ctx, "activity")
	}
	if want("q3") {
		q3Success(ctx)
		q3Fail(ctx)
		q3Cancel(ctx)
		w = q3Kill(ctx, w)
		q3Terminate(ctx)
		q3Stall(ctx)
	}

	fmt.Printf("\n=== DONE: %d failure(s)\n", failures)
	if failures > 0 {
		return fmt.Errorf("%d assertion(s) failed", failures)
	}
	return nil
}

func q2abc(ctx context.Context) {
	const id = "application/app-1"
	section("Q2a: second BackupWorkflow start with same ID while running (ConflictPolicy=FAIL)")
	run1, err := c.ExecuteWorkflow(ctx, appOpts(id, true), BackupWorkflow, BackupInput{AppID: "app-1", ProtectSeconds: 6})
	check(err == nil, "first start ok: id=%s run=%s", id, run1.GetRunID())

	_, err = c.ExecuteWorkflow(ctx, appOpts(id, true), BackupWorkflow, BackupInput{AppID: "app-1", ProtectSeconds: 1})
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	check(errors.As(err, &already), "second start rejected: %T: %v", err, err)
	if already != nil {
		check(already.RunId == run1.GetRunID(), "error carries running RunId=%s (for 'operation already in progress' UI)", already.RunId)
	}
	check(temporal.IsWorkflowExecutionAlreadyStartedError(err), "temporal.IsWorkflowExecutionAlreadyStartedError(err)==true")

	section("Q2a': SDK footgun - WorkflowExecutionErrorWhenAlreadyStarted=false (the default)")
	r, err := c.ExecuteWorkflow(ctx, appOpts(id, false), BackupWorkflow, BackupInput{AppID: "app-1"})
	check(err == nil && r.GetRunID() == run1.GetRunID(),
		"with default flag, ExecuteWorkflow returns err=%v and silently hands back the EXISTING run %s", err, r.GetRunID())
	r, err = c.ExecuteWorkflow(ctx, appOpts(id, false), RestoreWorkflow, "app-1")
	check(err == nil && r.GetRunID() == run1.GetRunID(),
		"...even for a different workflow type (RestoreWorkflow): err=%v run=%s", err, r.GetRunID())

	section("Q2b: RestoreWorkflow (different type) with same ID while BackupWorkflow runs")
	_, err = c.ExecuteWorkflow(ctx, appOpts(id, true), RestoreWorkflow, "app-1")
	check(errors.As(err, &already), "restore start rejected: %v", err)

	section("Q2c: after first completes, same ID can be started again (ReusePolicy=ALLOW_DUPLICATE)")
	var res BackupResult
	check(run1.Get(ctx, &res) == nil, "first backup completed")
	run2, err := c.ExecuteWorkflow(ctx, appOpts(id, true), RestoreWorkflow, "app-1")
	check(err == nil, "new start (RestoreWorkflow) accepted: run=%s", run2.GetRunID())
	check(run2.Get(ctx, nil) == nil, "restore completed")
	run3, err := c.ExecuteWorkflow(ctx, appOpts(id, true), BackupWorkflow, BackupInput{AppID: "app-1", ProtectSeconds: 1})
	check(err == nil, "new start (BackupWorkflow) accepted: run=%s", run3.GetRunID())
	check(run3.Get(ctx, nil) == nil, "backup completed")
}

// triggerAndWait fires the schedule and returns the trigger workflow's result.
func triggerAndWait(ctx context.Context, h client.ScheduleHandle) (string, TriggerResult) {
	before, _ := h.Describe(ctx)
	n := len(before.Info.RecentActions)
	if err := h.Trigger(ctx, client.ScheduleTriggerOptions{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP}); err != nil {
		panic(err)
	}
	var act client.ScheduleActionResult
	for i := 0; i < 100; i++ {
		d, _ := h.Describe(ctx)
		if len(d.Info.RecentActions) > n {
			act = d.Info.RecentActions[len(d.Info.RecentActions)-1]
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if act.StartWorkflowResult == nil {
		panic("schedule did not start an action")
	}
	var tr TriggerResult
	if err := c.GetWorkflow(ctx, act.StartWorkflowResult.WorkflowID, act.StartWorkflowResult.FirstExecutionRunID).Get(ctx, &tr); err != nil {
		panic(err)
	}
	return act.StartWorkflowResult.WorkflowID, tr
}

func q2d(ctx context.Context, mode string) {
	const id = "application/app-1"
	section("Q2d (" + mode + "): Schedule -> ScheduledBackupTrigger -> BackupWorkflow{ID=" + id + "}")
	schedID := "backup/app-1/" + mode
	h, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:      schedID,
		Spec:    client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: 24 * time.Hour}}}, // fired manually below
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		Action: &client.ScheduleWorkflowAction{
			ID:        "schedule-trigger/app-1/" + mode, // Temporal appends "-<timestamp>"
			Workflow:  ScheduledBackupTrigger,
			Args:      []any{TriggerInput{AppID: "app-1", Mode: mode}},
			TaskQueue: TaskQueue,
		},
	})
	check(err == nil, "schedule %s created (err=%v)", schedID, err)
	defer func() { _ = h.Delete(ctx) }()

	manual, err := c.ExecuteWorkflow(ctx, appOpts(id, true), BackupWorkflow, BackupInput{AppID: "app-1", ProtectSeconds: 6})
	check(err == nil, "manual backup running: run=%s", manual.GetRunID())

	trigID, tr := triggerAndWait(ctx, h)
	fmt.Printf("  trigger workflow ID = %s\n", trigID)
	check(strings.HasPrefix(trigID, "schedule-trigger/app-1/"+mode+"-"), "schedule-started workflow ID has timestamp suffix (so it cannot be application/app-1)")
	check(tr.Outcome == "skipped_overlap", "schedule fired while manual running -> outcome=%s detail=%q conflictRun=%s", tr.Outcome, tr.ConflictDetail, tr.RunID)
	check(status(ctx, id, manual.GetRunID()) == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, "manual backup unaffected (still running)")

	check(manual.Get(ctx, nil) == nil, "manual backup completed")
	trigID, tr = triggerAndWait(ctx, h)
	check(tr.Outcome == "started", "schedule fired while idle -> outcome=%s run=%s (trigger %s)", tr.Outcome, tr.RunID, trigID)
	d, err := c.DescribeWorkflowExecution(ctx, id, tr.RunID)
	check(err == nil && d.WorkflowExecutionInfo.Type.Name == "BackupWorkflow", "application/app-1 run %s is a BackupWorkflow", tr.RunID)
	if mode == "child" {
		pe := d.WorkflowExecutionInfo.GetParentExecution()
		fmt.Printf("  child's parent execution = %v\n", pe.GetWorkflowId())
	}
	check(c.GetWorkflow(ctx, id, tr.RunID).Get(ctx, nil) == nil, "scheduled BackupWorkflow completed (outlived its trigger: ABANDON)")
}

func startQ3(ctx context.Context, app string, in BackupInput) client.WorkflowRun {
	in.AppID = app
	run, err := c.ExecuteWorkflow(ctx, appOpts("application/"+app, true), BackupWorkflow, in)
	if err != nil {
		panic(err)
	}
	return run
}

func report(ctx context.Context, run client.WorkflowRun) []string {
	trail := activityTrail(ctx, run.GetID(), run.GetRunID())
	fmt.Printf("  history: %s\n", strings.Join(trail, " -> "))
	return trail
}

func q3Success(ctx context.Context) {
	section("Q3 baseline: success path")
	run := startQ3(ctx, "app-q3-ok", BackupInput{ProtectSeconds: 2})
	check(run.Get(ctx, nil) == nil, "completed")
	trail := report(ctx, run)
	check(count(trail, "completed:Resume") == 1, "Resume ran exactly once")
}

func q3Fail(ctx context.Context) {
	section("Q3(i): later activity fails permanently")
	run := startQ3(ctx, "app-q3-fail", BackupInput{ProtectSeconds: 5, FailProtect: true})
	err := run.Get(ctx, nil)
	check(err != nil, "workflow failed: %v", err)
	trail := report(ctx, run)
	check(status(ctx, run.GetID(), run.GetRunID()) == enumspb.WORKFLOW_EXECUTION_STATUS_FAILED, "status FAILED")
	check(count(trail, "completed:Resume") == 1, "compensation Resume completed")
}

func q3Cancel(ctx context.Context) {
	section("Q3(ii): workflow cancelled by client mid-way")
	run := startQ3(ctx, "app-q3-cancel", BackupInput{ProtectSeconds: 30})
	waitFor(ctx, run.GetID(), run.GetRunID(), "completed:Quiesce", 20*time.Second)
	time.Sleep(2 * time.Second)
	check(c.CancelWorkflow(ctx, run.GetID(), run.GetRunID()) == nil, "cancel requested")
	err := run.Get(ctx, nil)
	var ce *temporal.CanceledError
	check(errors.As(err, &ce), "run.Get -> CanceledError: %v", err)
	trail := report(ctx, run)
	check(status(ctx, run.GetID(), run.GetRunID()) == enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED, "status CANCELED")
	check(count(trail, "completed:Resume") == 1, "compensation Resume completed")
	check(indexOf(trail, "canceled:ProtectVolumes") >= 0 && indexOf(trail, "canceled:ProtectVolumes") < indexOf(trail, "scheduled:Resume"),
		"ProtectVolumes acknowledged cancel BEFORE Resume was scheduled (WaitForCancellation=true)")
}

func q3Kill(ctx context.Context, w *workerProc) *workerProc {
	section("Q3(iii): worker process SIGKILLed mid-ProtectVolumes, restarted")
	run := startQ3(ctx, "app-q3-kill", BackupInput{ProtectSeconds: 8})
	waitFor(ctx, run.GetID(), run.GetRunID(), "completed:Quiesce", 20*time.Second)
	time.Sleep(3 * time.Second)
	w.kill()
	killed := time.Now()
	time.Sleep(5 * time.Second)
	w = startWorker()
	var res BackupResult
	err := run.Get(ctx, &res)
	check(err == nil, "workflow completed after restart (%.1fs after kill), err=%v", time.Since(killed).Seconds(), err)
	trail := report(ctx, run)
	check(res.ProtectAttempt >= 2, "ProtectVolumes retried after heartbeat timeout: final attempt=%d", res.ProtectAttempt)
	check(res.ResumedFrom > 0, "retry resumed from heartbeat checkpoint=%d (not from 0)", res.ResumedFrom)
	check(count(trail, "completed:Resume") == 1, "Resume ran exactly once")
	return w
}

func q3Terminate(ctx context.Context) {
	section("Q3(iv): workflow TERMINATED mid-way (expect: NO compensation)")
	run := startQ3(ctx, "app-q3-term", BackupInput{ProtectSeconds: 30})
	waitFor(ctx, run.GetID(), run.GetRunID(), "completed:Quiesce", 20*time.Second)
	time.Sleep(2 * time.Second)
	check(c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "operator terminate") == nil, "terminate requested")
	time.Sleep(5 * time.Second)
	trail := report(ctx, run)
	check(status(ctx, run.GetID(), run.GetRunID()) == enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, "status TERMINATED")
	check(count(trail, "scheduled:Resume") == 0, "Resume was NEVER scheduled -> app left quiesced (Layer 2 dead-man switch must cover)")
	r2, err := c.ExecuteWorkflow(ctx, appOpts(run.GetID(), true), RestoreWorkflow, "app-q3-term")
	check(err == nil && r2.Get(ctx, nil) == nil, "terminated run released the application ID (new start ok)")
}

func q3Stall(ctx context.Context) {
	section("Q3(v): stalled ProtectVolumes (no heartbeats), MaxAttempts=1 -> heartbeat timeout -> compensation")
	start := time.Now()
	run := startQ3(ctx, "app-q3-stall", BackupInput{ProtectSeconds: 5, StallProtect: true, ProtectMaxAttempts: 1})
	err := run.Get(ctx, nil)
	var te *temporal.TimeoutError
	check(errors.As(err, &te) && te.TimeoutType() == enumspb.TIMEOUT_TYPE_HEARTBEAT,
		"failed with HEARTBEAT timeout after %.1fs (StartToClose is 60m): %v", time.Since(start).Seconds(), err)
	trail := report(ctx, run)
	check(count(trail, "completed:Resume") == 1, "compensation Resume completed")

	section("Q3(v'): stalled ProtectVolumes with retries -> retried from checkpoint, completes")
	run = startQ3(ctx, "app-q3-stall-retry", BackupInput{ProtectSeconds: 4, StallProtect: true})
	var res BackupResult
	err = run.Get(ctx, &res)
	check(err == nil && res.ProtectAttempt == 2, "completed on attempt %d, resumed from checkpoint %d (err=%v)", res.ProtectAttempt, res.ResumedFrom, err)
	report(ctx, run)
}
