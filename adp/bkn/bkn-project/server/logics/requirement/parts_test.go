// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// withPartRunes sets the size of a part for one test.
func withPartRunes(runes int) func() {
	before := maxPartRunes
	maxPartRunes = runes
	return func() { maxPartRunes = before }
}

func Test_SplitDocuments(t *testing.T) {
	Convey("Documents are read in parts of whole blocks\n", t, func() {
		defer withPartRunes(10)()
		long := doc("long", interfaces.REQUIREMENT_DOC_PRD, "aaaa", "bbbb", "cccc", "dddddddddddddd")
		short := doc("short", interfaces.REQUIREMENT_DOC_OTHER, "ee")

		parts := splitDocuments([]*interfaces.RequirementDocument{long, short})

		blocks := [][]string{}
		for _, p := range parts {
			names := []string{}
			for _, d := range p.docs {
				for _, b := range d.Blocks {
					names = append(names, d.ID+"/"+b.ID)
				}
			}
			blocks = append(blocks, names)
		}
		// A long document is split between blocks, a block longer than a part is a part of its own,
		// and a short document shares the part it fits in.
		So(blocks, ShouldResemble, [][]string{{"long/b1", "long/b2"}, {"long/b3"}, {"long/b4"}, {"short/b1"}})
		So(parts[0].docs[0].Content, ShouldEqual, long.Content)
		So(long.Blocks, ShouldHaveLength, 4)

		// Keys are the same while the text is, and differ between parts.
		again := splitDocuments([]*interfaces.RequirementDocument{long, short})
		So(partKeys(again), ShouldResemble, partKeys(parts))
		So(parts[0].key, ShouldNotEqual, parts[1].key)
		So(pendingParts(again, []string{parts[2].key, "gone"}), ShouldHaveLength, 1)
	})

	Convey("Small documents are one part\n", t, func() {
		So(splitDocuments(testDocuments()), ShouldHaveLength, 1)
	})
}

func Test_ExtractConceptsInParts(t *testing.T) {
	Convey("A long set of documents is read in parts; a part that fails is read again next time\n", t, func() {
		defer withPartRunes(170)()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		ra.EXPECT().SuggestDocumentType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		docs := testDocuments()
		st := &runStore{}
		st.expect(ra)
		expectRun := func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(testDraft(), true, nil)
			// Tables are listed once a run, whatever its parts.
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{}, nil)
		}
		answers := map[string]string{
			"prd": `{"concepts":[{"id":"a","term":"Agreed date","category":"property","citations":[{"document_id":"prd","quote":"updates the agreed date"}]}]}`,
			"pol": `{"concepts":[{"id":"a","term":"Signed change","category":"event","citations":[{"document_id":"pol","quote":"only a signed change does"}]}]}`,
		}
		polFails := true
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				// Parts are asked side by side: no assertion here, off the test's goroutine.
				var msg conceptsMessage
				if err := json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg); err != nil ||
					len(msg.Documents) != 1 {
					return nil, errors.New("not one document a part")
				}
				id := msg.Documents[0].ID
				if id == "pol" && polFails {
					return nil, errors.New("connection refused")
				}
				return &interfaces.BknAgentTask{TaskID: "t-" + id, Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: answers[id]}, nil
			}).AnyTimes()

		expectRun()
		_, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)

		So(err, ShouldBeNil)
		run := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
		// What the part that answered gave is written; the run names the part still to read.
		So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
		// Why the part still to read failed.
		So(run.Error, ShouldEqual, failureAgentUnavailable)
		So(run.PendingParts, ShouldHaveLength, 1)
		So(run.Inputs, ShouldNotBeNil)
		So(ids(st.req.Spec.Concepts, conceptID), ShouldResemble, []string{"c1"})

		// The next run reads only that part, over what the first part gave.
		polFails = false
		expectRun()
		_, err = svc.ExtractConcepts(ctx, token, "p1", caller, false)

		So(err, ShouldBeNil)
		run = st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
		So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
		So(run.Error, ShouldEqual, "")
		So(run.PendingParts, ShouldBeEmpty)
		So(run.AgentTaskID, ShouldEqual, "t-pol")
		So(ids(st.req.Spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2"})
	})

	Convey("A run whose parts all fail fails, and keeps the parts the last run left to read\n", t, func() {
		defer withPartRunes(170)()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		docs := testDocuments()
		pending := partKeys(splitDocuments(docs))[1:]
		st := &runStore{req: &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY,
			Spec: interfaces.RequirementSpec{Runs: map[string]*interfaces.RequirementRun{interfaces.REQUIREMENT_RUN_CONCEPTS: {
				Status: interfaces.REQUIREMENT_RUN_READY, Error: failureAgentTimeout, PendingParts: pending,
				Inputs: requirementInputsOf(docs, owned.Project)}}}}}
		st.expect(ra)
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{}, nil)
		// Cut off at the model's output cap: not asked the same again, but half by half, without
		// reasoning; the halves are cut off too (as the OpenAI SDK says it).
		var asked []*interfaces.BknAgentRunRequest
		var mu sync.Mutex
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				mu.Lock()
				defer mu.Unlock()
				asked = append(asked, req)
				detail := "StructuredOutputError: structured output failed: not supported natively and the prompt " +
					"fallback was still invalid (output truncated at the output limit)"
				if len(asked) > 1 {
					detail = "LengthFinishReasonError: Could not parse response content as the length limit was reached"
				}
				return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_FAILED, FailureDetail: detail}, nil
			}).Times(3)

		_, err := svc.ExtractConcepts(context.Background(), token, "p1", caller, false)

		So(err, ShouldBeNil)
		run := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
		So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_FAILED)
		So(run.Error, ShouldEqual, failureOutputTooLong)
		So(run.Retried, ShouldBeTrue)
		So(run.PendingParts, ShouldResemble, pending)
		for _, req := range asked {
			So(req.ThinkingMode, ShouldEqual, "disabled")
		}
		// The halves read one block each.
		halves := []string{asked[1].Message, asked[2].Message}
		if !strings.Contains(halves[0], "Requests by email") {
			halves[0], halves[1] = halves[1], halves[0]
		}
		So(halves[0], ShouldContainSubstring, "Requests by email")
		So(halves[0], ShouldNotContainSubstring, "within 2 working days")
		So(halves[1], ShouldContainSubstring, "within 2 working days")
		So(halves[1], ShouldNotContainSubstring, "Requests by email")
	})

	Convey("A part cut off at the output cap is read half by half, and what the halves give is written\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		ra.EXPECT().SuggestDocumentType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		docs := testDocuments()[1:]
		st := &runStore{}
		st.expect(ra)
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{}, nil)
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				whole := strings.Contains(req.Message, "Requests by email") && strings.Contains(req.Message, "within 2 working days")
				switch {
				case whole:
					return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_FAILED,
						FailureDetail: "LengthFinishReasonError: length limit was reached"}, nil
				case strings.Contains(req.Message, "Requests by email"):
					return &interfaces.BknAgentTask{TaskID: "t2", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
						Output: `{"concepts":[{"id":"a","term":"Signed change","category":"event","citations":[{"document_id":"pol","quote":"only a signed change does"}]}]}`}, nil
				default:
					return &interfaces.BknAgentTask{TaskID: "t3", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
						Output: `{"concepts":[{"id":"a","term":"Reply time","category":"property","citations":[{"document_id":"pol","quote":"within 2 working days"}]}]}`}, nil
				}
			}).Times(3)

		_, err := svc.ExtractConcepts(context.Background(), token, "p1", caller, false)

		So(err, ShouldBeNil)
		run := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
		So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
		So(run.Error, ShouldEqual, "")
		So(run.PendingParts, ShouldBeEmpty)
		So(ids(st.req.Spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2"})
	})

	Convey("Documents added or changed since the run that left parts to read are all read again\n", t, func() {
		defer withPartRunes(170)()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		docs := testDocuments()
		pending := partKeys(splitDocuments(docs))[1:]
		// The run that left a part read only the first document.
		read := requirementInputsOf(docs[:1], owned.Project)
		st := &runStore{req: &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY,
			Spec: interfaces.RequirementSpec{Runs: map[string]*interfaces.RequirementRun{interfaces.REQUIREMENT_RUN_CONCEPTS: {
				Status: interfaces.REQUIREMENT_RUN_READY, Error: failureAgentTimeout, PendingParts: pending, Inputs: read}}}}}
		st.expect(ra)
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{}, nil)
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
			Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{}`}, nil).Times(2)

		_, err := svc.ExtractConcepts(context.Background(), token, "p1", caller, false)

		So(err, ShouldBeNil)
		run := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
		So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
		So(run.PendingParts, ShouldBeEmpty)
		So(run.Inputs.Documents, ShouldHaveLength, 2)
	})
}
