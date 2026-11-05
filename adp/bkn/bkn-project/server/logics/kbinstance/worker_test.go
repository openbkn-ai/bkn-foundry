// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type queueMemory struct {
	*processMemory
	runnableErr error
}

func (q *queueMemory) List(context.Context, string, int) ([]*interfaces.KBBuild, error) {
	return nil, nil
}
func (q *queueMemory) Runnable(context.Context, int) ([]interfaces.KBBuildKey, error) {
	return []interfaces.KBBuildKey{{ProjectID: "project", ID: "job"}}, q.runnableErr
}
func (q *queueMemory) Cancel(context.Context, string, string, interfaces.AccountInfo, int64) (bool, error) {
	return false, nil
}

type draftReader struct {
	buildDraft
	interfaces.DraftAccess
}

func (d draftReader) GetDraft(ctx context.Context, project string) (*interfaces.Draft, bool, error) {
	return d.buildDraft.GetDraft(ctx, project)
}

type capturedLog struct{ errors, infos []string }

func (c *capturedLog) log() *WorkerLog {
	return &WorkerLog{
		Name:   "build",
		Errorf: func(f string, a ...any) { c.errors = append(c.errors, fmt.Sprintf(f, a...)) },
		Infof:  func(f string, a ...any) { c.infos = append(c.infos, fmt.Sprintf(f, a...)) },
	}
}

func workerFixture(t *testing.T, access kb.WorkerAccess) (*buildWorker, *processMemory, *fileRuntime, *capturedLog) {
	t.Helper()
	c, store, runtime, sink := coordinatorFixture(t)
	captured := &capturedLog{}
	worker := &buildWorker{
		queue: &queueMemory{processMemory: store}, drafts: draftReader{buildDraft: c.Drafts.(buildDraft)}, lease: "worker", log: captured.log(),
		access: func() (kb.Providers, kb.WorkerAccess) {
			return kb.Providers{Builds: runtime, Datasets: datasetsWithRecords{sink}}, access
		},
	}
	return worker, store, runtime, captured
}

type datasetsWithRecords struct{ *recordSink }

func (datasetsWithRecords) Catalog(context.Context, string) (string, error)           { return "", nil }
func (datasetsWithRecords) Ensure(context.Context, interfaces.DraftDatasetSpec) error { return nil }

func TestWorkerPausesOnUnknownLicenceWithoutTouchingTheBuild(t *testing.T) {
	worker, store, runtime, log := workerFixture(t, kb.WorkerPause)
	revision := store.record.Build.Revision
	for i := 0; i < 3; i++ {
		worker.tick(context.Background())
	}
	if store.record.Build.State != "queued" || store.record.Build.Revision != revision || runtime.revokes != 0 || runtime.releases != 0 || runtime.starts != 0 || runtime.checks != 0 {
		t.Fatalf("an unknown licence changed work: %+v", store.record.Build.State)
	}
	if len(log.infos) != 1 || !strings.Contains(log.infos[0], "paused") {
		t.Fatalf("the pause must be logged exactly once, got %v", log.infos)
	}
}

func TestWorkerCleansOnlyOnADefiniteNoLicence(t *testing.T) {
	worker, store, runtime, _ := workerFixture(t, kb.WorkerClean)
	worker.tick(context.Background())
	if store.record.Build.State != "cancelled" || runtime.revokes != 1 || runtime.releases != 1 || runtime.starts != 0 {
		t.Fatalf("state %s revokes %d", store.record.Build.State, runtime.revokes)
	}
}

func TestWorkerResumesAfterThePauseAndLogsIt(t *testing.T) {
	worker, store, runtime, log := workerFixture(t, kb.WorkerPause)
	worker.tick(context.Background())
	worker.access = func() (kb.Providers, kb.WorkerAccess) {
		return kb.Providers{Builds: runtime, Datasets: datasetsWithRecords{&recordSink{rows: map[string]map[string]any{}}}}, kb.WorkerRun
	}
	worker.tick(context.Background())
	if runtime.starts != 1 || store.record.Build.Files[0].AgentTaskID == "" {
		t.Fatal("work did not resume on the same journal")
	}
	joined := strings.Join(log.infos, "\n")
	if !strings.Contains(joined, "resumed") || !strings.Contains(joined, "queued -> running") {
		t.Fatalf("log = %s", joined)
	}
}

func TestWorkerLogsStepFailureOnceAndItsRecovery(t *testing.T) {
	worker, _, runtime, log := workerFixture(t, kb.WorkerRun)
	runtime.checkErr = errors.New("source\nunavailable")
	for i := 0; i < 3; i++ {
		worker.tick(context.Background())
	}
	if len(log.errors) != 1 || !strings.Contains(log.errors[0], "project/job") || strings.Contains(log.errors[0], "\n") {
		t.Fatalf("errors = %q", log.errors)
	}
	runtime.checkErr = nil
	worker.tick(context.Background())
	if !strings.Contains(strings.Join(log.infos, "\n"), "project/job recovered") {
		t.Fatalf("infos = %q", log.infos)
	}
}

func TestWorkerReportsQueueFailureAndMissingStorage(t *testing.T) {
	worker, store, _, log := workerFixture(t, kb.WorkerRun)
	worker.queue = &queueMemory{processMemory: store, runnableErr: errors.New("database down")}
	worker.tick(context.Background())
	worker.tick(context.Background())
	if len(log.errors) != 1 || !strings.Contains(log.errors[0], "database down") {
		t.Fatalf("errors = %q", log.errors)
	}
	worker.access = func() (kb.Providers, kb.WorkerAccess) { return kb.Providers{Builds: &fileRuntime{}}, kb.WorkerRun }
	worker.tick(context.Background())
	if len(log.errors) != 2 || !strings.Contains(log.errors[1], "dataset") {
		t.Fatalf("errors = %q", log.errors)
	}
}

func TestWorkerLogNeverKeepsLongOrMultilineDetail(t *testing.T) {
	long := strings.Repeat("x", 2000)
	if got := safeDetail(errors.New(long + "\nsecond line")); len(got) > 310 || strings.Contains(got, "\n") {
		t.Fatalf("detail not bounded: %d", len(got))
	}
}

func deadlineFixture(t *testing.T) (*Coordinator, *processMemory, *fileRuntime, *time.Time) {
	t.Helper()
	c, store, runtime, _ := coordinatorFixture(t)
	now := time.Now().Add(time.Hour)
	c.Now = func() time.Time { return now }
	runtime.task.Status = interfaces.BKN_AGENT_TASK_RUNNING
	step(t, c)
	if store.record.Build.Files[0].State != "waiting" {
		t.Fatalf("fixture state %s", store.record.Build.Files[0].State)
	}
	return c, store, runtime, &now
}

func TestAgentTaskPastItsDeadlineEndsRecoverably(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pollErr error
	}{{"still running", nil}, {"task service gone", errors.New("connection refused")}} {
		t.Run(tc.name, func(t *testing.T) {
			c, store, runtime, now := deadlineFixture(t)
			runtime.pollErr = tc.pollErr
			*now = now.Add(AgentTaskDeadline() + time.Millisecond)
			if err := c.Step(context.Background(), "project", "job", "worker"); err != nil {
				t.Fatal(err)
			}
			file := store.record.Build.Files[0]
			if file.State != "failed" || file.Reason != "extraction_timed_out" || store.record.Build.State != "attention" || runtime.starts != 1 {
				t.Fatalf("file %+v build %s", file, store.record.Build.State)
			}
			if submissionUncertain(store.record.Build) {
				t.Fatal("a timed-out file has a known task and must not block the others")
			}
		})
	}
}

func TestAgentTaskWithinItsDeadlineKeepsWaiting(t *testing.T) {
	c, store, runtime, now := deadlineFixture(t)
	*now = now.Add(AgentTaskDeadline())
	step(t, c)
	if store.record.Build.Files[0].State != "waiting" || runtime.polls != 1 {
		t.Fatalf("state %s", store.record.Build.Files[0].State)
	}
	runtime.pollErr = errors.New("blip")
	if err := c.Step(context.Background(), "project", "job", "worker"); err == nil {
		t.Fatal("a poll failure within the deadline is retried, not swallowed")
	}
	if store.record.Build.Files[0].State != "waiting" {
		t.Fatal("a blip must not end the task")
	}
}

func TestFinishedTaskIsAcceptedEvenAfterTheDeadline(t *testing.T) {
	c, store, runtime, now := deadlineFixture(t)
	*now = now.Add(2 * AgentTaskDeadline())
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	step(t, c)
	if store.record.Build.Files[0].State != "validating" {
		t.Fatalf("state %s", store.record.Build.Files[0].State)
	}
}

func TestLegacyWaitingFileGetsItsDeadlineFromFirstSight(t *testing.T) {
	c, store, runtime, now := deadlineFixture(t)
	store.record.Build.Files[0].SubmittedAt = 0
	*now = now.Add(24 * time.Hour)
	step(t, c)
	file := store.record.Build.Files[0]
	if file.State != "waiting" || file.SubmittedAt != now.UnixMilli() || runtime.polls != 1 {
		t.Fatalf("file %+v", file)
	}
}

func TestProgressMarkIgnoresPollsAndMovesOnRealChange(t *testing.T) {
	c, store, _, now := deadlineFixture(t)
	first := store.record.Build.ProgressTime
	if first == 0 {
		t.Fatal("progress was not recorded")
	}
	*now = now.Add(time.Minute)
	step(t, c)
	if store.record.Build.ProgressTime != first {
		t.Fatal("a poll that changed nothing must not look like progress")
	}
	*now = now.Add(AgentTaskDeadline() + time.Minute)
	step(t, c)
	if store.record.Build.ProgressTime < now.UnixMilli() {
		t.Fatal("a file ending is progress")
	}
}

func TestSummarizeWorkReportsTheLongestStall(t *testing.T) {
	summary := SummarizeWork([]interfaces.KBWorkItem{{State: "running", ProgressTime: 100_000}, {State: "running", ProgressTime: 40_000}, {State: "attention", ProgressTime: 90_000}}, 100_000)
	if summary.Pending != 3 || summary.ByState["running"] != 2 || summary.ByState["attention"] != 1 || summary.StuckSeconds != 60 || summary.AtSampleLimit {
		t.Fatalf("%+v", summary)
	}
	if empty := SummarizeWork(nil, 100_000); empty.Pending != 0 || empty.StuckSeconds != 0 {
		t.Fatalf("%+v", empty)
	}
	if full := SummarizeWork(make([]interfaces.KBWorkItem, workSampleLimit), 0); !full.AtSampleLimit {
		t.Fatal("a capped reading must say so")
	}
}

func TestConfiguredDeadlineFallsBackRatherThanNeverExpiring(t *testing.T) {
	t.Cleanup(func() { ConfigureAgentTaskDeadline(0) })
	ConfigureAgentTaskDeadline(time.Minute)
	if AgentTaskDeadline() != time.Minute {
		t.Fatal("setting ignored")
	}
	ConfigureAgentTaskDeadline(-time.Second)
	if AgentTaskDeadline() != defaultAgentTaskDeadline {
		t.Fatal("a non-positive setting must not disable the deadline")
	}
}

func TestTaskOverdueDecision(t *testing.T) {
	late := int64(1) + AgentTaskDeadline().Milliseconds() + 2
	running := &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_RUNNING}
	finished := &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_SUCCEEDED}
	failed := &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_FAILED}
	gone := errors.New("gone")
	for _, tc := range []struct {
		name      string
		submitted int64
		task      *interfaces.BknAgentTask
		err       error
		now       int64
		want      bool
	}{
		{"running late", 1, running, nil, late, true},
		{"running in time", 1, running, nil, 2, false},
		{"unreachable late", 1, finished, gone, late, true},
		{"unreachable in time", 1, nil, gone, 2, false},
		{"finished late", 1, finished, nil, late, false},
		{"failed late is reported by the task itself", 1, failed, nil, late, false},
		{"never stamped", 0, running, nil, late, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TaskOverdue(&interfaces.KBFileBuild{SubmittedAt: tc.submitted}, tc.task, tc.err, tc.now); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
