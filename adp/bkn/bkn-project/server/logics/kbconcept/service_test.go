// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"context"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"go.uber.org/mock/gomock"
)

type workflowStore struct {
	interfaces.KBResourceAccess
	interfaces.DraftAccess
	selection *interfaces.KBSelection
	draft     *interfaces.Draft
	written   *interfaces.Draft
	conflict  bool
}

func (s *workflowStore) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	return &interfaces.KBResourceState{Current: s.selection}, nil
}
func (s *workflowStore) GetDraft(context.Context, string) (*interfaces.Draft, bool, error) {
	return s.draft, s.draft != nil, nil
}
func (s *workflowStore) CommitDocumentDraft(_ context.Context, d *interfaces.Draft, g interfaces.DocumentDraftGuard) (bool, error) {
	if s.conflict || s.selection.Revision != g.SelectionRevision {
		return false, nil
	}
	s.written = d
	return true, nil
}

type workflowSource struct {
	kb.Source
	calls         int
	denyOnRecheck bool
}

func (s *workflowSource) SummarizeFiles(context.Context, kb.SourceScope, kb.FileQuery) (kb.FileSummary, error) {
	return kb.FileSummary{}, nil
}
func (s *workflowSource) ReadSampleChunks(_ context.Context, refs []kb.SampleVersion, q kb.PageQuery) (kb.SamplePage, error) {
	s.calls++
	if s.denyOnRecheck && s.calls > 1 {
		return kb.SamplePage{}, &kb.SourceError{StatusCode: 403}
	}
	ref := refs[0]
	return kb.SamplePage{Entries: []kb.SampleChunk{{SampleVersion: ref, ChunkID: "1", Text: "订单 O-1", Located: "exact", Spans: []kb.Span{{BlockID: "b1", Start: 0, End: 6}}}}}, nil
}

type workflowConcepts struct {
	fn    func(kb.ConceptRequest) (kb.ConceptProposal, error)
	calls int
}

func (c *workflowConcepts) Extract(_ context.Context, r kb.ConceptRequest) (kb.ConceptProposal, error) {
	c.calls++
	return c.fn(r)
}
func TestConceptWorkflowPreservesDraftAndRejectsLateResults(t *testing.T) {
	for _, scenario := range []string{"new", "existing", "stale_request", "selection_changed", "draft_changed", "denied", "invalid_quote", "empty", "not_owner", "archived", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			owner := interfaces.AccountInfo{ID: "u", Type: "user"}
			project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner, NamingLanguage: "zh-CN"}}
			if scenario == "not_owner" {
				project.Owner.ID = "other"
			}
			if scenario == "archived" {
				project.Archived = true
			}
			ps := pmock.NewMockProjectService(ctrl)
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").AnyTimes().Return(project, nil)
			store := &workflowStore{selection: &interfaces.KBSelection{ID: "lock", ProjectID: "p", State: "active", Revision: 7, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", SampleRole: "debug", ChunkCount: 1}, {KnowledgeBaseID: "1", FileID: "42", ContentVersionID: "9", SampleRole: "acceptance", ChunkCount: 1}}}}
			req := ExtractRequest{SelectionID: "lock", SelectionRevision: 7}
			if scenario == "existing" {
				store.draft = &interfaces.Draft{ProjectID: "p", Status: "ready", UpdateTime: 11, Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "existing", Name: "Human name", Adopted: true}}, Decisions: []*interfaces.DraftDecision{{Kind: "object_types", ItemID: "existing", Action: "adopt"}}}}
				req.DraftUpdateTime = 11
			}
			if scenario == "stale_request" {
				req.SelectionRevision = 6
			}
			source := &workflowSource{denyOnRecheck: scenario == "denied"}
			concepts := &workflowConcepts{fn: func(r kb.ConceptRequest) (kb.ConceptProposal, error) {
				if len(r.Samples) != 1 || r.Samples[0].Citation.FileID != "41" || r.Caller != owner {
					t.Fatal("held-out content or wrong operator reached Agent")
				}
				if scenario == "selection_changed" {
					store.selection.Revision++
				}
				if scenario == "draft_changed" {
					store.conflict = true
				}
				if scenario == "unavailable" {
					return kb.ConceptProposal{}, kb.ErrUnavailable
				}
				if scenario == "empty" {
					return kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{}, RelationTypes: []*interfaces.DraftRelationType{}}, nil
				}
				input := &SampleInput{Request: r}
				object := proposedObject(input, "order_doc", "entity")
				if scenario == "invalid_quote" {
					object.Document.Citations[0].Quote = "not present"
				}
				return kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}}, nil
			}}
			result, err := NewService(ps, store, store, source, concepts).Extract(context.Background(), "Bearer synthetic", "p", owner, req)
			success := scenario == "new" || scenario == "existing" || scenario == "empty"
			if success != (err == nil) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if scenario == "empty" {
				if store.written != nil || result.Draft != nil || result.AddedObjects != 0 {
					t.Fatal("empty result changed draft")
				}
				return
			}
			if !success {
				if store.written != nil {
					t.Fatal("failure wrote a draft")
				}
				return
			}
			if store.written == nil || result.AddedObjects != 4 {
				t.Fatal("verified models and fixed evidence templates not saved")
			}
			for _, o := range store.written.Items.ObjectTypes {
				if o.ID == "order_doc" && (o.Adopted || o.Document.Citations[0].Quote != "") {
					t.Fatal("AI adoption or quote policy bypass")
				}
			}
			if scenario == "existing" && (store.written.Items.ObjectTypes[0].Name != "Human name" || !store.written.Items.ObjectTypes[0].Adopted || len(store.written.Items.Decisions) != 1 || len(store.draft.Items.ObjectTypes) != 1) {
				t.Fatal("human decisions or caller snapshot mutated")
			}
		})
	}
}

func TestSamplePreviewReadsOnlyDebugProofWithoutModelOrWrite(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ps := pmock.NewMockProjectService(ctrl)
	owner := interfaces.AccountInfo{ID: "u", Type: "user"}
	ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner, NamingLanguage: "en-US"}}, nil)
	store := &workflowStore{selection: &interfaces.KBSelection{ID: "lock", State: "active", Revision: 7, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", SampleRole: "debug", ChunkCount: 1, Name: "Order.pdf"}, {KnowledgeBaseID: "1", FileID: "42", ContentVersionID: "9", SampleRole: "acceptance", ChunkCount: 1, Name: "Held-out.pdf"}}}}
	source := &workflowSource{}
	result, err := NewService(ps, store, nil, source, nil).Samples(context.Background(), "Bearer synthetic", "p", owner, "lock", 7)
	if err != nil {
		t.Fatal(err)
	}
	if store.written != nil || len(result.Fragments) != 1 || result.Fragments[0].Citation.FileID != "41" || result.Fragments[0].Citation.Quote != "" || result.Fragments[0].Text != "订单 O-1" || result.Fragments[0].FileName != "Order.pdf" {
		t.Fatal("editor preview leaked held-out samples or persisted originals")
	}
}
