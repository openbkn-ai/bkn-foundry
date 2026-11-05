// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func cloneProcess(r *interfaces.KBBuildRecord) *interfaces.KBBuildRecord {
	next := *r
	raw, _ := json.Marshal(r.Build)
	next.Build = &interfaces.KBBuild{}
	_ = json.Unmarshal(raw, next.Build)
	return &next
}

type processMemory struct {
	record           *interfaces.KBBuildRecord
	active, failSave bool
}

func (m *processMemory) Create(_ context.Context, r *interfaces.KBBuildRecord) (bool, error) {
	m.record = cloneProcess(r)
	return true, nil
}
func (m *processMemory) Get(context.Context, string, string) (*interfaces.KBBuildRecord, error) {
	return cloneProcess(m.record), nil
}
func (m *processMemory) Claim(_ context.Context, _, _, lease string, _, until int64) (*interfaces.KBBuildRecord, error) {
	if m.record.Identity.Cleaned {
		return nil, nil
	}
	if !m.active {
		m.record.Build.State = "cancel_pending"
	}
	m.record.LeaseID, m.record.LeaseUntil = lease, until
	return cloneProcess(m.record), nil
}
func (m *processMemory) Save(_ context.Context, r *interfaces.KBBuildRecord, revision, _ int64) (bool, error) {
	if m.failSave {
		m.failSave = false
		return false, errors.New("lost process acknowledgement")
	}
	if m.record.Build.Revision != revision || (!m.active && r.Build.State != "cancel_pending" && r.Build.State != "cancelled") {
		return false, nil
	}
	m.record = cloneProcess(r)
	return true, nil
}

type buildDraft struct{ draft *interfaces.Draft }

func (d buildDraft) GetDraft(context.Context, string) (*interfaces.Draft, bool, error) {
	return d.draft, true, nil
}

type fileRuntime struct {
	starts, polls, checks, materializations, cleanups int
	startErr, materializeErr, checkErr, pollErr       error
	task                                              *interfaces.BknAgentTask
	writes                                            []kb.DatasetWrite
	afterStart, afterCheck                            func()
	afterComplete                                     func()
	revokes, releases                                 int
	input                                             *kb.FileInput
	readErrs                                          map[string]error
	startFor                                          func(index int) (*interfaces.BknAgentTask, error)
	checkErrs                                         map[string]error
}

func (*fileRuntime) Version(context.Context, interfaces.AccountInfo) (int, error) { return 3, nil }

func (r *fileRuntime) Delegate(_ context.Context, _ *interfaces.KBBuild, id interfaces.KBBuildIdentity) (interfaces.KBBuildIdentity, error) {
	id.PrincipalID = "job-app"
	id.Revision = 1
	return id, nil
}
func (*fileRuntime) Hold(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error {
	return nil
}
func (r *fileRuntime) Check(_ context.Context, _ *interfaces.KBBuild, _ interfaces.KBBuildIdentity, file interfaces.KBResourceFile) error {
	r.checks++
	if r.afterCheck != nil {
		r.afterCheck()
	}
	if err := r.checkErrs[file.FileID]; err != nil {
		return err
	}
	return r.checkErr
}
func (r *fileRuntime) ReadFile(_ context.Context, b *interfaces.KBBuild, _ interfaces.KBBuildIdentity, index int) (*kb.FileInput, error) {
	if err := r.readErrs[b.Files[index].File.FileID]; err != nil {
		return nil, err
	}
	if r.input != nil {
		return r.input, nil
	}
	ref := fileRef(b.Files[index].File)
	return &kb.FileInput{Blocks: []kb.EvidenceBlock{{SampleVersion: ref, BlockID: "test-block", Start: 0, End: 1}}, Text: map[string]string{"test-block": "x"}}, nil
}

func TestCoordinatorChangedModelCleansOldJobWithoutAnyExtraction(t *testing.T) {
	c, store, runtime, sink := coordinatorFixture(t)
	c.Drafts.(buildDraft).draft.Items.ObjectTypes[0].Name = "Changed after confirmation"
	step(t, c)
	if store.record.Build.State != "cancelled" || !store.record.Identity.Cleaned || runtime.revokes != 1 || runtime.releases != 1 || runtime.starts != 0 || sink.calls != 0 {
		t.Fatal("stale model job was left runnable or performed work")
	}
}

func TestCoordinatorCleanupOnlyNeedsNoDraftOrDatasetAccess(t *testing.T) {
	c, store, runtime, sink := coordinatorFixture(t)
	c.Drafts, c.Records = nil, nil
	if err := c.Stop(context.Background(), "project", "job", "cleanup-worker"); err != nil {
		t.Fatal(err)
	}
	if store.record.Build.State != "cancelled" || !store.record.Identity.Cleaned || runtime.starts != 0 || runtime.checks != 0 || sink.calls != 0 {
		t.Fatal("cleanup-only path accessed source contents or continued extraction")
	}
}

func TestCoordinatorImageOnlyRegistersEvidenceAndCountsAsSuccess(t *testing.T) {
	build, draft, _, _ := extractionFixture(t, "inspection", true)
	ref := fileRef(build.Files[0].File)
	input := &kb.FileInput{Text: map[string]string{}, Media: []kb.EvidenceMedia{{SampleVersion: ref, MediaID: "original-resource", OccurrenceID: "original-position", ImageRef: "original-position", Kind: "original", MimeType: "image/jpeg"}}}
	build.Files[0].InputDigest, build.Files[0].AgentTaskID = "", ""
	build.State = "queued"
	store := &processMemory{active: true, record: &interfaces.KBBuildRecord{Build: build, Identity: interfaces.KBBuildIdentity{GrantID: "grant", PrincipalID: "job-app", Revision: 1}}}
	runtime := &fileRuntime{input: input}
	sink := &recordSink{rows: map[string]map[string]any{}}
	c := &Coordinator{Store: store, Drafts: buildDraft{draft}, Runtime: runtime, Records: sink}
	for i := 0; i < 4; i++ {
		step(t, c)
	}
	if runtime.starts != 0 || runtime.polls != 0 || runtime.materializations != 0 || store.record.Build.State != "succeeded" || store.record.Build.Files[0].State != "evidence_only" || !store.record.Identity.Cleaned || sink.calls == 0 {
		t.Fatal("image evidence was lost or mistaken for business understanding", store.record.Build.State, sink.calls)
	}
}
func (r *fileRuntime) Start(_ context.Context, _ *interfaces.KBBuild, _ interfaces.KBBuildIdentity, index int, _ *interfaces.Draft, _ *kb.FileInput) (*interfaces.BknAgentTask, error) {
	r.starts++
	if r.afterStart != nil {
		r.afterStart()
	}
	if r.startFor != nil {
		return r.startFor(index)
	}
	return r.task, r.startErr
}
func (r *fileRuntime) Poll(_ context.Context, _ interfaces.KBBuildIdentity, id string) (*interfaces.BknAgentTask, error) {
	r.polls++
	if r.startFor != nil && r.pollErr == nil {
		task := *r.task
		task.TaskID = id
		return &task, nil
	}
	return r.task, r.pollErr
}
func (r *fileRuntime) Materialize(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity, int, *interfaces.Draft, *kb.FileInput, *interfaces.BknAgentTask) ([]kb.DatasetWrite, error) {
	r.materializations++
	return r.writes, r.materializeErr
}
func (r *fileRuntime) Revoke(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error {
	r.revokes++
	return nil
}
func (r *fileRuntime) ReleaseHolds(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error {
	r.releases++
	return nil
}
func (r *fileRuntime) Complete(context.Context, *interfaces.KBBuild, interfaces.KBBuildIdentity) error {
	r.cleanups++
	if r.afterComplete != nil {
		r.afterComplete()
	}
	return nil
}

type recordSink struct {
	prunes   int
	calls    int
	failOnce bool
	rows     map[string]map[string]any
}

func (s *recordSink) Prune(ctx context.Context, _ interfaces.DraftDatasetSpec, _ kb.DatasetFileScope, _ []string, guard func(context.Context) error) error {
	s.prunes++
	return guard(ctx)
}

func (s *recordSink) Replace(_ context.Context, spec interfaces.DraftDatasetSpec, row map[string]any) error {
	s.calls++
	s.rows[spec.ResourceID+"/"+row["_id"].(string)] = row
	if s.failOnce {
		s.failOnce = false
		return errors.New("uncertain replace completion")
	}
	return nil
}
func coordinatorFixture(t *testing.T) (*Coordinator, *processMemory, *fileRuntime, *recordSink) {
	t.Helper()
	draft, selection := buildPlanFixture(t)
	draft.Items.ObjectTypes[0].Properties = append(draft.Items.ObjectTypes[0].Properties, &interfaces.DraftProperty{Name: "model_fingerprint", Type: "string", ReadOnly: true})
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", selection.Owner)
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "ready"
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	draft, err = kbdataset.BindPreview(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	build, err := PlanFileBuild("job", "lineage", "debug", 3, draft, selection)
	if err != nil {
		t.Fatal(err)
	}
	build.State = "queued"
	store := &processMemory{active: true, record: &interfaces.KBBuildRecord{Build: build, Identity: interfaces.KBBuildIdentity{GrantID: "grant", PrincipalID: "job-app", Revision: 1}}}
	v := 3
	p := 2
	runtime := &fileRuntime{task: &interfaces.BknAgentTask{TaskID: "single-task", AgentID: kb.DocumentExtractionAgentID, AgentVersion: &v, PromptVersion: &p, ModelID: "model", Status: "succeeded", Output: "private output never stored", FailureDetail: "private failure never stored"}}
	row := map[string]any{}
	for _, field := range plan.Datasets[0].Layout.Fields {
		row[field.OriginalName] = nil
	}
	row["_id"], row["model_fingerprint"], row["extraction_version"] = "row", build.ModelFingerprint, "fixture-version"
	runtime.writes = []kb.DatasetWrite{{Spec: plan.Datasets[0], Row: row}}
	sink := &recordSink{rows: map[string]map[string]any{}}
	return &Coordinator{Store: store, Drafts: buildDraft{draft}, Runtime: runtime, Records: sink, Now: func() time.Time { return time.UnixMilli(100) }}, store, runtime, sink
}
func step(t *testing.T, c *Coordinator) {
	t.Helper()
	if err := c.Step(context.Background(), "project", "job", "worker"); err != nil {
		t.Fatal(err)
	}
}
func TestCoordinatorCompletesKnownTaskAndCleansDelegationWithoutPersistingOutput(t *testing.T) {
	c, store, runtime, sink := coordinatorFixture(t)
	for i := 0; i < 6; i++ {
		step(t, c)
	}
	if runtime.starts != 1 || sink.calls != 1 || runtime.cleanups != 1 || store.record.Build.State != "succeeded" || !store.record.Identity.Cleaned {
		t.Fatal("build did not complete exactly once", runtime.starts, sink.calls, store.record.Build.State)
	}
	raw, _ := json.Marshal(store.record.Build)
	for _, secret := range []string{"private output", "private failure", "job-app", "grant"} {
		if contains(string(raw), secret) {
			t.Fatal("private runtime data entered process DTO")
		}
	}
}
func contains(a, b string) bool {
	for i := 0; i+len(b) <= len(a); i++ {
		if a[i:i+len(b)] == b {
			return true
		}
	}
	return false
}
func TestCoordinatorNeverResubmitsLostSubmissionOrAcknowledgement(t *testing.T) {
	for _, scenario := range []string{"lost_remote_response", "lost_process_save"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, runtime, sink := coordinatorFixture(t)
			if scenario == "lost_remote_response" {
				runtime.startErr = errors.New("transport failed")
			} else {
				runtime.afterStart = func() { store.failSave = true }
			}
			_ = c.Step(context.Background(), "project", "job", "worker")
			for i := 0; i < 4; i++ {
				step(t, c)
			}
			if runtime.starts != 1 || sink.calls != 0 || store.record.Build.Files[0].State != "submission_unknown" || store.record.Build.State != "needs_review" || !store.record.Identity.Cleaned {
				t.Fatal("unknown inference was retried or hidden", runtime.starts, store.record.Build.State)
			}
		})
	}
}
func TestCoordinatorReplaysUncertainStableRowWithoutAnotherInference(t *testing.T) {
	c, store, runtime, sink := coordinatorFixture(t)
	sink.failOnce = true
	step(t, c)
	if err := c.Step(context.Background(), "project", "job", "worker"); err == nil {
		t.Fatal("uncertain write hidden")
	}
	if store.record.Build.Files[0].RowsWritten != 0 {
		t.Fatal("uncertain completion counted as durable")
	}
	for i := 0; i < 4; i++ {
		step(t, c)
	}
	if sink.calls != 2 || len(sink.rows) != 1 || runtime.starts != 1 || store.record.Build.State != "succeeded" {
		t.Fatal("recovery duplicated instance or inference")
	}
}
func TestCoordinatorRejectsEntireInvalidWriteSetBeforeFirstReplace(t *testing.T) {
	for _, scenario := range []string{"foreign_dataset", "missing_model", "duplicate_row", "changed_agent", "missing_actual_version", "invalid_output"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, runtime, sink := coordinatorFixture(t)
			step(t, c)
			switch scenario {
			case "foreign_dataset":
				runtime.writes[0].Spec.ResourceID = "foreign"
			case "missing_model":
				delete(runtime.writes[0].Row, "model_fingerprint")
			case "duplicate_row":
				runtime.writes = append(runtime.writes, runtime.writes[0])
			case "changed_agent":
				runtime.task.AgentID = "different"
			case "missing_actual_version":
				runtime.task.AgentVersion = nil
			case "invalid_output":
				runtime.materializeErr = errors.New("quote is not grounded")
			}
			step(t, c)
			if sink.calls != 0 || store.record.Build.Files[0].State != "failed" {
				t.Fatal("invalid result partly written")
			}
		})
	}
}
func TestCoordinatorStopsBeforeWriteOnPermissionOrCurrentModelChange(t *testing.T) {
	for _, scenario := range []string{"permission_revoked", "source_changed", "draft_changed", "cancelled_before_write"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, runtime, sink := coordinatorFixture(t)
			step(t, c)
			switch scenario {
			case "permission_revoked":
				runtime.checkErr = errors.New("permission revoked")
			case "source_changed":
				store.active = false
			case "draft_changed":
				c.Drafts.(buildDraft).draft.UpdateTime++
			case "cancelled_before_write":
				runtime.afterCheck = func() { store.active = false }
			}
			_ = c.Step(context.Background(), "project", "job", "worker")
			if sink.calls != 0 {
				t.Fatal("revoked or stale result written")
			}
		})
	}
}

func TestCleanupCrashAfterIdentityDisableNeverRepeatsSourceRelease(t *testing.T) {
	c, store, runtime, _ := coordinatorFixture(t)
	for i := 0; i < 3; i++ {
		step(t, c)
	}
	runtime.afterComplete = func() { store.failSave = true }
	if err := c.Step(context.Background(), "project", "job", "worker"); err == nil {
		t.Fatal("lost cleanup acknowledgement hidden")
	}
	if store.record.Identity.CleanupPhase != "holds_released" || store.record.Identity.Cleaned {
		t.Fatal("cleanup checkpoint not durable")
	}
	runtime.afterComplete = nil
	step(t, c)
	if runtime.releases != 1 || runtime.revokes != 1 || runtime.cleanups != 2 || store.record.Build.State != "succeeded" {
		t.Fatal("disabled identity used to repeat source cleanup")
	}
}

func TestBuildStopsWhenActualModelChangesBetweenFiles(t *testing.T) {
	c, store, runtime, sink := coordinatorFixture(t)
	store.record.Build.Files = append(store.record.Build.Files, interfaces.KBFileBuild{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "11", ContentVersionID: "101"}, State: "pending"})
	step(t, c)
	step(t, c)
	runtime.task.TaskID = "second-task"
	runtime.task.ModelID = "different-model"
	step(t, c)
	if runtime.starts != 2 || sink.calls != 1 || store.record.Build.Files[1].State != "failed" || store.record.Build.Files[1].Reason != "execution_changed" {
		t.Fatal("mixed model execution was accepted")
	}
}
func TestBuildCannotWriteWhenActualPromptOrModelIdentityIsUnknown(t *testing.T) {
	for _, field := range []string{"prompt", "model"} {
		t.Run(field, func(t *testing.T) {
			c, store, runtime, sink := coordinatorFixture(t)
			if field == "prompt" {
				runtime.task.PromptVersion = nil
			} else {
				runtime.task.ModelID = ""
			}
			step(t, c)
			if sink.calls != 0 || store.record.Build.Files[0].Reason != "execution_version_unknown" {
				t.Fatal("unknown actual configuration used")
			}
		})
	}
}
