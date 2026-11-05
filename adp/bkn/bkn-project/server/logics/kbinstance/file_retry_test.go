// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// batchFixture is a batch of three files of any domain, with a controllable clock.
func batchFixture(t *testing.T) (*Coordinator, *processMemory, *fileRuntime, *recordSink, *time.Time) {
	t.Helper()
	c, store, runtime, sink := coordinatorFixture(t)
	first := store.record.Build.Files[0]
	store.record.Build.Files = nil
	for _, id := range []string{"invoice-1", "survey-2", "manual-3"} {
		file := first
		file.File.FileID, file.File.ContentVersionID = id, id+"-v1"
		store.record.Build.Files = append(store.record.Build.Files, file)
	}
	now := time.Now().Add(time.Hour)
	c.Now = func() time.Time { return now }
	return c, store, runtime, sink, &now
}

func runUntil(t *testing.T, c *Coordinator, store *processMemory, now *time.Time, done func() bool) {
	t.Helper()
	for i := 0; i < 200 && !done(); i++ {
		if err := c.Step(context.Background(), "project", "job", "worker"); err != nil && !errors.Is(err, errTransient) && !errors.Is(err, ErrBuildChanged) {
			t.Fatal(err)
		}
		*now = now.Add(5 * time.Minute)
	}
	if !done() {
		t.Fatalf("did not settle: %s %+v", store.record.Build.State, store.record.Build.Files)
	}
}

var errTransient = errors.New("transient")

func cleaned(store *processMemory) func() bool {
	return func() bool { return store.record.Identity.Cleaned }
}

func taskWith(c *fileRuntime, id string, status string) *interfaces.BknAgentTask {
	task := *c.task
	task.TaskID, task.Status = id, status
	return &task
}

func TestFailedTaskRetriesThatFileOnlyThenSucceeds(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	attempts := map[int]int{}
	runtime.startFor = func(index int) (*interfaces.BknAgentTask, error) {
		attempts[index]++
		if index == 1 && attempts[index] == 1 {
			return taskWith(runtime, "failed-first", interfaces.BKN_AGENT_TASK_FAILED), nil
		}
		return taskWith(runtime, fmt.Sprintf("task-%d-%d", index, attempts[index]), interfaces.BKN_AGENT_TASK_SUCCEEDED), nil
	}
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runUntil(t, c, store, now, cleaned(store))
	if attempts[0] != 1 || attempts[2] != 1 || attempts[1] != 2 {
		t.Fatalf("attempts %v", attempts)
	}
	if store.record.Build.State != "succeeded" || store.record.Build.Files[1].Attempts != 1 {
		t.Fatalf("state %s file %+v", store.record.Build.State, store.record.Build.Files[1])
	}
}

func TestRetryWaitsForItsBackoffWithoutFinishingTheBuild(t *testing.T) {
	c, store, _, _, now := batchFixture(t)
	file := &store.record.Build.Files[0]
	file.State, file.Reason = "failed", "extraction_failed"
	store.record.Build.Files = store.record.Build.Files[:1]
	step(t, c)
	if file := store.record.Build.Files[0]; file.NextAttempt != now.UnixMilli()+fileBackoff(0) || file.State != "failed" {
		t.Fatalf("file %+v", file)
	}
	step(t, c)
	if store.record.Build.State == "finishing" || store.record.Identity.Cleaned {
		t.Fatal("a build must not end while a retry is scheduled")
	}
	*now = now.Add(fileRetryDelay)
	step(t, c)
	if file := store.record.Build.Files[0]; file.State != "pending" || file.Attempts != 1 || file.AgentTaskID != "" {
		t.Fatalf("file %+v", file)
	}
}

func TestRetriesAreBoundedAndEndWithThatFileReported(t *testing.T) {
	c, store, runtime, sink, now := batchFixture(t)
	store.record.Build.Files = store.record.Build.Files[:2]
	starts := map[int]int{}
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runtime.startFor = func(index int) (*interfaces.BknAgentTask, error) {
		starts[index]++
		if index == 0 {
			return taskWith(runtime, fmt.Sprintf("always-fails-%d", starts[index]), interfaces.BKN_AGENT_TASK_FAILED), nil
		}
		return taskWith(runtime, "fine", interfaces.BKN_AGENT_TASK_SUCCEEDED), nil
	}
	runUntil(t, c, store, now, cleaned(store))
	if starts[0] != maxFileAttempts || starts[1] != 1 {
		t.Fatalf("starts %v", starts)
	}
	bad, good := store.record.Build.Files[0], store.record.Build.Files[1]
	if bad.State != "failed" || bad.Reason != "extraction_failed" || !bad.Cleared || good.State != "succeeded" || store.record.Build.State != "needs_review" {
		t.Fatalf("bad %+v good %+v state %s", bad, good, store.record.Build.State)
	}
	if sink.prunes == 0 {
		t.Fatal("a failed file must not keep rows")
	}
}

func TestLostSubmissionNoLongerStopsLaterFilesAndIsNeverResent(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	starts := map[int]int{}
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runtime.startFor = func(index int) (*interfaces.BknAgentTask, error) {
		starts[index]++
		if index == 0 {
			return nil, errors.New("connection reset after the request was sent")
		}
		return taskWith(runtime, fmt.Sprintf("task-%d", index), interfaces.BKN_AGENT_TASK_SUCCEEDED), nil
	}
	runUntil(t, c, store, now, cleaned(store))
	files := store.record.Build.Files
	if starts[0] != 1 || files[0].State != "submission_unknown" || files[1].State != "succeeded" || files[2].State != "succeeded" {
		t.Fatalf("starts %v files %+v", starts, files)
	}
}

func TestSubmissionFailureHoldsTheNextSubmissionsBack(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	runtime.startErr = errors.New("uncertain")
	step(t, c)
	starts := runtime.starts
	if store.record.Build.SubmitHoldUntil != now.UnixMilli()+fileRetryDelay.Milliseconds() {
		t.Fatalf("hold %d", store.record.Build.SubmitHoldUntil)
	}
	step(t, c)
	if runtime.starts != starts {
		t.Fatal("an outage must not spend the remaining files")
	}
	*now = now.Add(fileRetryDelay)
	runtime.startErr = nil
	runtime.task.Status = interfaces.BKN_AGENT_TASK_RUNNING
	step(t, c)
	if runtime.starts != starts+1 || store.record.Build.SubmitFailures != 0 || store.record.Build.SubmitHoldUntil != 0 {
		t.Fatalf("starts %d failures %d", runtime.starts, store.record.Build.SubmitFailures)
	}
}

func TestDefinitelyRefusedSubmissionIsRetriedThenReported(t *testing.T) {
	for name, refusal := range map[string]error{
		"runtime says so": fmt.Errorf("wrap: %w", kb.ErrNotSubmitted),
		"cannot connect":  fmt.Errorf("run: %w", &net.OpError{Op: "dial", Err: errors.New("refused")}),
		"service refuses": &interfaces.BknAgentError{HTTPCode: 400},
	} {
		t.Run(name, func(t *testing.T) {
			c, store, runtime, _, now := batchFixture(t)
			store.record.Build.Files = store.record.Build.Files[:1]
			runtime.startErr = refusal
			runUntil(t, c, store, now, cleaned(store))
			if file := store.record.Build.Files[0]; runtime.starts != maxFileAttempts || file.State != "failed" || file.Reason != reasonSubmissionRefused {
				t.Fatalf("starts %d file %+v", runtime.starts, file)
			}
		})
	}
}

func TestSubmissionRefusedClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&interfaces.BknAgentError{HTTPCode: 503}, false},
		{&interfaces.BknAgentError{HTTPCode: 429}, true},
		{&net.OpError{Op: "read", Err: errors.New("reset")}, false},
		{errors.New("unknown"), false},
	} {
		if got := submissionRefused(tc.err); got != tc.want {
			t.Fatalf("%v: got %v", tc.err, got)
		}
	}
}

func TestTransientReadOfOneFileIsBoundedAndLetsOthersFinish(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runtime.readErrs = map[string]error{"invoice-1": errTransient}
	runtime.startFor = func(index int) (*interfaces.BknAgentTask, error) {
		return taskWith(runtime, fmt.Sprintf("task-%d", index), interfaces.BKN_AGENT_TASK_SUCCEEDED), nil
	}
	runUntil(t, c, store, now, cleaned(store))
	files := store.record.Build.Files
	if files[0].State != "failed" || files[0].Reason != reasonSourceUnavailable || files[0].Transient != maxFileAttempts || files[0].Attempts != 0 || files[1].State != "succeeded" || files[2].State != "succeeded" {
		t.Fatalf("files %+v", files)
	}
}

func TestVersionGoneFailsThatFileOnlyButLostAccessStopsTheBuild(t *testing.T) {
	for name, gone := range map[string]error{"version gone": kb.ErrVersionGone, "410": &kb.SourceError{StatusCode: 410}, "404": &kb.SourceError{StatusCode: 404}} {
		t.Run(name, func(t *testing.T) {
			c, store, runtime, _, now := batchFixture(t)
			runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
			runtime.checkErrs = map[string]error{"survey-2": gone}
			runtime.startFor = func(index int) (*interfaces.BknAgentTask, error) {
				return taskWith(runtime, fmt.Sprintf("task-%d", index), interfaces.BKN_AGENT_TASK_SUCCEEDED), nil
			}
			runUntil(t, c, store, now, cleaned(store))
			files := store.record.Build.Files
			if files[1].State != "failed" || files[1].Reason != reasonSourceVersionGone || files[0].State != "succeeded" || files[2].State != "succeeded" || store.record.Build.State != "needs_review" {
				t.Fatalf("state %s files %+v", store.record.Build.State, files)
			}
		})
	}
	c, store, runtime, _, now := batchFixture(t)
	runtime.checkErrs = map[string]error{"survey-2": &kb.SourceError{StatusCode: 403}, "invoice-1": &kb.SourceError{StatusCode: 403}}
	runUntil(t, c, store, now, cleaned(store))
	if store.record.Build.State != "cancelled" {
		t.Fatalf("a revoked person must stop the build, got %s", store.record.Build.State)
	}
}

func TestUnparsableFileIsReportedAndOthersContinue(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runtime.readErrs = map[string]error{"manual-3": fmt.Errorf("parse: %w", kb.ErrInputNotExtractable)}
	runtime.startFor = func(index int) (*interfaces.BknAgentTask, error) {
		return taskWith(runtime, fmt.Sprintf("task-%d", index), interfaces.BKN_AGENT_TASK_SUCCEEDED), nil
	}
	runUntil(t, c, store, now, cleaned(store))
	files := store.record.Build.Files
	if files[2].Reason != "source_requires_review" || files[2].State != "failed" || files[0].State != "succeeded" || files[1].State != "succeeded" {
		t.Fatalf("files %+v", files)
	}
}

func TestAcceptanceSampleStillNeedsAScoreSoEvidenceAloneIsNotEnough(t *testing.T) {
	build := &interfaces.KBBuild{Mode: "acceptance", Files: []interfaces.KBFileBuild{{State: "evidence_only"}}}
	if aggregate := aggregateAcceptance(build.Files); aggregate != nil {
		t.Fatal("no score for evidence")
	}
}

func completedStep(id, version string, state string, model string, prompt int) interfaces.KBFileBuild {
	agent := 3
	digest := strings.Repeat("a", 64)
	return interfaces.KBFileBuild{File: interfaces.KBResourceFile{KnowledgeBaseID: "kb", FileID: id, ContentVersionID: version}, State: state, AgentTaskID: "t-" + id, AgentVersion: &agent, PromptVersion: &prompt, ModelID: model, InputDigest: digest, RowsDigest: digest, RowsWritten: 2}
}

func reuseBuild(files ...interfaces.KBFileBuild) *interfaces.KBBuild {
	return &interfaces.KBBuild{ID: "new", Mode: "full", GenerationID: "g", ModelFingerprint: "f", AgentVersion: 3, Files: files}
}

func priorBuild(id string, files ...interfaces.KBFileBuild) *interfaces.KBBuild {
	return &interfaces.KBBuild{ID: id, GenerationID: "g", ModelFingerprint: "f", AgentVersion: 3, Files: files}
}

func pendingStep(id, version string) interfaces.KBFileBuild {
	return interfaces.KBFileBuild{File: interfaces.KBResourceFile{KnowledgeBaseID: "kb", FileID: id, ContentVersionID: version}, State: "pending"}
}

func TestReuseCarriesOnlyTheSameVersionAndExecution(t *testing.T) {
	done := completedStep("contract", "v1", "succeeded", "model-a", 2)
	cases := []struct {
		name   string
		mutate func(new *interfaces.KBBuild, prior *interfaces.KBBuild)
		reused bool
	}{
		{"same version and model", func(*interfaces.KBBuild, *interfaces.KBBuild) {}, true},
		{"new content version", func(n, _ *interfaces.KBBuild) { n.Files[0].File.ContentVersionID = "v2" }, false},
		{"original inclusion changed", func(n, _ *interfaces.KBBuild) { n.Files[0].File.IncludeOriginal = true }, false},
		{"other generation", func(_, p *interfaces.KBBuild) { p.GenerationID = "other" }, false},
		{"changed ontology", func(_, p *interfaces.KBBuild) { p.ModelFingerprint = "other" }, false},
		{"other agent version", func(_, p *interfaces.KBBuild) { p.AgentVersion = 4 }, false},
		{"pinned other model", func(n, _ *interfaces.KBBuild) { n.ModelID = "model-b" }, false},
		{"pinned same model", func(n, _ *interfaces.KBBuild) { n.ModelID = "model-a" }, true},
		{"pinned other prompt", func(n, _ *interfaces.KBBuild) { v := 9; n.PromptVersion = &v }, false},
		{"failed before", func(_, p *interfaces.KBBuild) { p.Files[0].State = "failed" }, false},
		{"acceptance measures fresh", func(n, _ *interfaces.KBBuild) { n.Mode = "acceptance" }, false},
		{"no digest", func(_, p *interfaces.KBBuild) { p.Files[0].RowsDigest = "" }, false},
		{"no task", func(_, p *interfaces.KBBuild) { p.Files[0].AgentTaskID = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build, prior := reuseBuild(pendingStep("contract", "v1")), priorBuild("old", done)
			tc.mutate(build, prior)
			ReuseCompleted(build, []*interfaces.KBBuild{prior})
			file := build.Files[0]
			if tc.reused != (file.State == "succeeded") {
				t.Fatalf("state %s", file.State)
			}
			if tc.reused && (file.ReusedFrom != "old" || file.AgentTaskID != "t-contract" || file.RowsDigest == "" || file.ModelID != "model-a" || file.RowsWritten != 2) {
				t.Fatalf("receipt not carried: %+v", file)
			}
			if !tc.reused && file.ReusedFrom != "" {
				t.Fatalf("file %+v", file)
			}
		})
	}
}

func TestReuseTrustsOnlyTheNewestBuildThatTouchedTheFile(t *testing.T) {
	done := completedStep("patent", "v1", "succeeded", "m", 2)
	failed := done
	failed.State = "failed"
	build := reuseBuild(pendingStep("patent", "v1"))
	ReuseCompleted(build, []*interfaces.KBBuild{priorBuild("newer", failed), priorBuild("older", done)})
	if build.Files[0].State != "pending" {
		t.Fatal("a newer failed attempt removed or replaced those rows")
	}
	build = reuseBuild(pendingStep("patent", "v1"))
	ReuseCompleted(build, []*interfaces.KBBuild{priorBuild("untouched", pendingStep("patent", "v1")), priorBuild("older", done)})
	if build.Files[0].ReusedFrom != "older" {
		t.Fatal("a build that never reached the file says nothing about it")
	}
	cleared := pendingStep("patent", "v1")
	cleared.Cleared = true
	build = reuseBuild(pendingStep("patent", "v1"))
	ReuseCompleted(build, []*interfaces.KBBuild{priorBuild("newer", cleared), priorBuild("older", done)})
	if build.Files[0].State != "pending" {
		t.Fatal("a build that never ran a file can still have removed its rows")
	}
	build = reuseBuild(pendingStep("patent", "v1"))
	ReuseCompleted(build, []*interfaces.KBBuild{priorBuild("newer", completedStep("patent", "v2", "succeeded", "m", 2)), priorBuild("older", done)})
	if build.Files[0].State != "pending" {
		t.Fatal("rows now belong to another version")
	}
}

func TestReuseKeepsEvidenceOnlyFiles(t *testing.T) {
	image := completedStep("scan", "v1", "evidence_only", "", 0)
	image.AgentTaskID = ""
	build := reuseBuild(pendingStep("scan", "v1"))
	ReuseCompleted(build, []*interfaces.KBBuild{priorBuild("old", image)})
	if build.Files[0].State != "evidence_only" {
		t.Fatalf("%+v", build.Files[0])
	}
}

func TestReusedFileGoesBackWhenTheBuildActuallyRanAnotherModel(t *testing.T) {
	reused := completedStep("a", "v1", "succeeded", "old-model", 2)
	reused.ReusedFrom = "old"
	build := reuseBuild(reused)
	build.ModelID = "new-model"
	if !settleReusedExecution(build) || build.Files[0].State != "pending" || build.Files[0].ReusedFrom != "" || build.Files[0].AgentTaskID != "" || build.Files[0].RowsWritten != 0 {
		t.Fatalf("%+v", build.Files[0])
	}
	same := completedStep("a", "v1", "succeeded", "m", 2)
	same.ReusedFrom = "old"
	build = reuseBuild(same)
	if settleReusedExecution(build) || build.ModelID != "m" || *build.PromptVersion != 2 {
		t.Fatal("an all-reused build takes its execution from what it reused")
	}
	other := completedStep("b", "v1", "succeeded", "n", 2)
	other.ReusedFrom = "old"
	build = reuseBuild(same, other)
	if !settleReusedExecution(build) || build.Files[0].State != "succeeded" || build.Files[1].State != "pending" {
		t.Fatal("reused files that disagree cannot share a build")
	}
	if build.ModelID != "" || build.PromptVersion != nil {
		t.Fatal("a reused file must not pin the model the requeued file will run on")
	}
	build.ModelID = "current"
	if !settleReusedExecution(build) || build.Files[0].State != "pending" {
		t.Fatal("once a real task pinned the build, the rest must align")
	}
}

func TestCoordinatorFinishesAllReusedBuildWithoutModelWork(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	for i := range store.record.Build.Files {
		id := store.record.Build.Files[i].File.FileID
		reused := completedStep(id, id+"-v1", "succeeded", "model", 2)
		reused.File = store.record.Build.Files[i].File
		reused.ReusedFrom = "old"
		store.record.Build.Files[i] = reused
	}
	runUntil(t, c, store, now, cleaned(store))
	if runtime.starts != 0 || runtime.polls != 0 || store.record.Build.State != "succeeded" {
		t.Fatalf("starts %d state %s", runtime.starts, store.record.Build.State)
	}
}

func TestPlanReportsFileWithoutVersionAndBuildsTheRest(t *testing.T) {
	draft, selection := buildPlanFixture(t)
	missing := selection.Files[0]
	missing.FileID, missing.ContentVersionID, missing.SampleRole = "no-version", "", "debug"
	selection.Files = append(selection.Files, missing)
	build, err := PlanFileBuild("job", "lineage", "debug", 3, draft, selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(build.Skipped) != 1 || build.Skipped[0].Reason != ReasonNoContentVersion || build.Skipped[0].File.FileID != "no-version" {
		t.Fatalf("%+v", build.Skipped)
	}
	for _, file := range build.Files {
		if file.File.ContentVersionID == "" {
			t.Fatal("a file with no version must not reach delegation")
		}
	}
	selection.Files = []interfaces.KBResourceFile{missing}
	if _, err := PlanFileBuild("job", "lineage", "debug", 3, draft, selection); !errors.Is(err, ErrInvalidBuild) {
		t.Fatal("a build of nothing is refused")
	}
}

func TestBuildWithSkippedFileEndsForReview(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	store.record.Build.Files = store.record.Build.Files[:1]
	store.record.Build.Skipped = []interfaces.KBFileBuild{{File: interfaces.KBResourceFile{FileID: "no-version"}, State: "failed", Reason: ReasonNoContentVersion}}
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runUntil(t, c, store, now, cleaned(store))
	if store.record.Build.State != "needs_review" || store.record.Build.Files[0].State != "succeeded" {
		t.Fatalf("state %s", store.record.Build.State)
	}
}

func TestAcceptFileExclusionRecordsExactlyWhatWasLeftOut(t *testing.T) {
	by := interfaces.AccountInfo{ID: "owner", Type: "user"}
	good := completedStep("good", "v1", "succeeded", "m", 2)
	image := completedStep("image", "v1", "evidence_only", "", 0)
	bad := completedStep("bad", "v1", "failed", "m", 2)
	bad.Reason = "source_requires_review"
	lost := pendingStep("lost", "v1")
	lost.State = "submission_unknown"
	untouched := pendingStep("untouched", "v1")
	build := &interfaces.KBBuild{Mode: "full", State: "needs_review", Files: []interfaces.KBFileBuild{good, image, bad, lost, untouched}, Skipped: []interfaces.KBFileBuild{{File: interfaces.KBResourceFile{FileID: "no-version"}, State: "failed", Reason: ReasonNoContentVersion}}}
	if err := AcceptFileExclusion(build, by, 77); err != nil {
		t.Fatal(err)
	}
	if build.State != "succeeded" || build.Files[0].State != "succeeded" || build.Files[1].State != "evidence_only" || build.Files[2].State != interfaces.KBFileExcluded {
		t.Fatalf("%+v", build)
	}
	got := map[string]string{}
	for _, file := range build.Acceptance.Files {
		got[file.File.FileID] = file.Reason
	}
	want := map[string]string{"bad": "source_requires_review", "lost": "not_processed", "untouched": "not_processed", "no-version": ReasonNoContentVersion}
	if len(got) != len(want) || build.Acceptance.By != by || build.Acceptance.Time != 77 {
		t.Fatalf("%+v", build.Acceptance)
	}
	for id, reason := range want {
		if id != "lost" && got[id] != reason {
			t.Fatalf("%s: %s", id, got[id])
		}
	}
}

func TestAcceptFileExclusionRefusals(t *testing.T) {
	by := interfaces.AccountInfo{ID: "owner", Type: "user"}
	good := completedStep("good", "v1", "succeeded", "m", 2)
	bad := completedStep("bad", "v1", "failed", "m", 2)
	for name, build := range map[string]*interfaces.KBBuild{
		"nil":             nil,
		"debug build":     {Mode: "debug", State: "needs_review", Files: []interfaces.KBFileBuild{good, bad}},
		"still running":   {Mode: "full", State: "running", Files: []interfaces.KBFileBuild{good, bad}},
		"nothing to list": {Mode: "full", State: "needs_review", Files: []interfaces.KBFileBuild{good}},
		"nothing usable":  {Mode: "full", State: "needs_review", Files: []interfaces.KBFileBuild{bad}},
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(AcceptFileExclusion(build, by, 1), ErrNothingToAccept) {
				t.Fatal("must refuse")
			}
		})
	}
}

func TestOnlyKnownExecutionFailuresRetry(t *testing.T) {
	for reason, want := range map[string]bool{"extraction_failed": true, ReasonExtractionTimedOut: true, "invalid_extraction": true, "source_requires_review": false, reasonSourceVersionGone: false, "execution_changed": false, reasonSubmissionRefused: false, "submission_unknown": false, "write_set_changed": false} {
		if retryableReason(reason) != want {
			t.Fatalf("%s", reason)
		}
	}
}

func TestPermanentFailureIsNotAttemptedAgain(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	store.record.Build.Files = store.record.Build.Files[:1]
	store.record.Build.Files[0].State, store.record.Build.Files[0].Reason = "failed", "source_requires_review"
	runUntil(t, c, store, now, cleaned(store))
	if runtime.starts != 0 || store.record.Build.Files[0].Attempts != 0 || store.record.Build.State != "needs_review" {
		t.Fatalf("starts %d %+v", runtime.starts, store.record.Build.Files[0])
	}
}

func TestSubmissionHoldGrowsWithRepeatedFailuresUpToALimit(t *testing.T) {
	build := &interfaces.KBBuild{}
	holdSubmissions(build, 1000)
	first := build.SubmitHoldUntil - 1000
	holdSubmissions(build, 1000)
	if second := build.SubmitHoldUntil - 1000; second != 2*first {
		t.Fatalf("first %d second %d", first, second)
	}
	for i := 0; i < 20; i++ {
		holdSubmissions(build, 1000)
	}
	if build.SubmitHoldUntil-1000 != maxSubmitHold.Milliseconds() {
		t.Fatalf("hold %d", build.SubmitHoldUntil-1000)
	}
}

func TestCoordinatorExtractsAgainWhatItReusedFromAnotherModel(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	store.record.Build.Files = store.record.Build.Files[:1]
	file := store.record.Build.Files[0].File
	reused := completedStep(file.FileID, file.ContentVersionID, "succeeded", "retired-model", 2)
	reused.File, reused.ReusedFrom, reused.InputDigest = file, "old", ""
	store.record.Build.Files[0] = reused
	store.record.Build.ModelID = "model"
	runtime.task.Status = interfaces.BKN_AGENT_TASK_SUCCEEDED
	runUntil(t, c, store, now, cleaned(store))
	if runtime.starts != 1 || store.record.Build.Files[0].ModelID != "model" || store.record.Build.Files[0].ReusedFrom != "" || store.record.Build.State != "succeeded" {
		t.Fatalf("starts %d %+v", runtime.starts, store.record.Build.Files[0])
	}
}

func TestAcceptanceBuildWithEvidenceOnlyFileNeedsReview(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	store.record.Build.Mode = "acceptance"
	store.record.Build.Files = store.record.Build.Files[:1]
	store.record.Build.Files[0].State = "evidence_only"
	c.Assessor = passingAssessor{}
	runUntil(t, c, store, now, cleaned(store))
	if store.record.Build.State != "needs_review" || runtime.starts != 0 {
		t.Fatalf("state %s", store.record.Build.State)
	}
}

type passingAssessor struct{}

func (passingAssessor) Guard(context.Context, *interfaces.KBBuild) error { return nil }
func (passingAssessor) Evaluate(context.Context, *interfaces.KBBuild, int, *interfaces.Draft, *kb.FileInput, []kb.DatasetWrite) (*AcceptanceCounts, error) {
	return &AcceptanceCounts{}, nil
}

func TestReadFailuresDoNotUseUpExtractionRetriesOrTheirBackoff(t *testing.T) {
	c, store, runtime, _, now := batchFixture(t)
	store.record.Build.Files = store.record.Build.Files[:1]
	runtime.readErrs = map[string]error{"invoice-1": errTransient}
	_ = c.Step(context.Background(), "project", "job", "worker")
	if file := store.record.Build.Files[0]; file.Transient != 1 || file.Attempts != 0 || file.NextAttempt == 0 {
		t.Fatalf("%+v", file)
	}
	*now = now.Add(time.Hour)
	runtime.readErrs = nil
	runtime.task.Status = interfaces.BKN_AGENT_TASK_FAILED
	step(t, c)
	if file := store.record.Build.Files[0]; file.State != "failed" || file.NextAttempt != 0 {
		t.Fatalf("a failure must not inherit an old retry time: %+v", file)
	}
	step(t, c)
	if file := store.record.Build.Files[0]; file.NextAttempt != now.UnixMilli()+fileBackoff(0) {
		t.Fatalf("the first retry takes the first backoff: %+v", file)
	}
}
