// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbconcept

import (
	"context"
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"go.uber.org/mock/gomock"
	"testing"
)

type recoveryConcepts struct {
	workflowConcepts
	recoverCalls int
	recoveredID  string
	recoverFn    func(kb.ConceptRequest) (kb.ConceptProposal, error)
}

func (c *recoveryConcepts) Recover(_ context.Context, r kb.ConceptRequest, id string) (kb.ConceptProposal, error) {
	c.recoverCalls++
	c.recoveredID = id
	return c.recoverFn(r)
}

func TestRecoverConceptsNeverReexecutesAndPreservesAllDraftGuards(t *testing.T) {
	const taskID = "c1111111-1111-4111-8111-111111111111"
	for _, scenario := range []string{"success", "unsupported", "invalid_id", "not_owner", "stale_selection", "permission_revoked", "late_selection", "invalid_quote"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			owner := interfaces.AccountInfo{ID: "u", Type: "user"}
			project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner, NamingLanguage: "zh-CN"}}
			if scenario == "not_owner" {
				project.Owner.ID = "other"
			}
			ps := pmock.NewMockProjectService(ctrl)
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").AnyTimes().Return(project, nil)
			store := &workflowStore{selection: &interfaces.KBSelection{ID: "lock", ProjectID: "p", State: "active", Revision: 7, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", SampleRole: "debug", ChunkCount: 1}}}}
			source := &workflowSource{denyOnRecheck: scenario == "permission_revoked"}
			provider := &recoveryConcepts{workflowConcepts: workflowConcepts{fn: func(kb.ConceptRequest) (kb.ConceptProposal, error) { return kb.ConceptProposal{}, kb.ErrUnavailable }}}
			provider.recoverFn = func(r kb.ConceptRequest) (kb.ConceptProposal, error) {
				object := proposedObject(&SampleInput{Request: r}, "order_doc", "entity")
				if scenario == "invalid_quote" {
					object.Document.Citations[0].Quote = "not supported"
				}
				if scenario == "late_selection" {
					store.selection.Revision++
				}
				return kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{object}}, nil
			}
			var request ExtractRequest
			if err := json.Unmarshal([]byte(`{"selection_id":"lock","selection_revision":7,"recover_task_id":"`+taskID+`"}`), &request); err != nil {
				t.Fatal(err)
			}
			if scenario == "invalid_id" {
				if err := json.Unmarshal([]byte(`{"recover_task_id":"not-a-uuid"}`), &request); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "stale_selection" {
				request.SelectionRevision = 6
			}
			var extractor kb.Concepts = provider
			if scenario == "unsupported" {
				extractor = &provider.workflowConcepts
			}
			result, err := NewService(ps, store, store, source, extractor).Extract(context.Background(), "Bearer synthetic", "p", owner, request)
			if provider.calls != 0 {
				t.Fatal("recovery fell back to a new model call")
			}
			switch scenario {
			case "unsupported", "invalid_id", "not_owner", "stale_selection":
				if provider.recoverCalls != 0 {
					t.Fatal("rejected request read a task before its authorization and snapshot guards")
				}
			default:
				if provider.recoverCalls != 1 {
					t.Fatal("authorized recovery did not exercise the provider before its later guard")
				}
			}
			if scenario != "success" {
				if err == nil || store.written != nil {
					t.Fatal("recovery bypassed an existing guard")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if provider.recoverCalls != 1 || provider.recoveredID != taskID || result.AddedObjects != 5 {
				t.Fatal("completed proposal was not recovered through the normal candidate pipeline")
			}
			for _, o := range result.Draft.Items.ObjectTypes {
				if o.Adopted {
					t.Fatal("recovered AI candidate was adopted")
				}
				if o.Document != nil {
					for _, q := range o.Document.Citations {
						if q.Quote != "" {
							t.Fatal("quote-off policy bypassed")
						}
					}
				}
			}
		})
	}
}
