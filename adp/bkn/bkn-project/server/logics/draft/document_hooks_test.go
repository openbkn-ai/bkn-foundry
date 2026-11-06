// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/licverify"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/docdraft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

// These tests pin, through the public draft service, what the built-in document hooks add to a
// draft beyond the community default: reading a draft, asking AI about document items, and
// confirming a draft with document items.

// evidenceDraft is a ready draft of document items without citations, so reading it reaches no
// source: a file, two evidence objects, an entity, and a contains relation that needs repair and
// carries a private business identity count.
func evidenceDraft() *interfaces.Draft {
	object := func(id, role string) *interfaces.DraftObjectType {
		return &interfaces.DraftObjectType{ID: id, Name: id, PrimaryKeys: []string{"_id"}, DisplayKey: "text",
			Document:   &interfaces.DraftDocumentModel{Role: role, Layer: "evidence", Reason: "Grounded " + id},
			Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true}, {Name: "text", Type: "string"}}}
	}
	return &interfaces.Draft{ProjectID: "p", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{object("file_doc", "file"), object("chunk", "evidence_chunk"),
				object("page", "evidence_page"), object("party", "entity")},
			RelationTypes: []*interfaces.DraftRelationType{{
				ID: "contains", Name: "Contains", Source: "file_doc", Target: "chunk", Cardinality: interfaces.DRAFT_CARDINALITY_ONE_TO_MANY,
				DocumentRepair: "target_identity",
				Document:       &interfaces.DraftDocumentModel{Role: "evidence_contains", Layer: "evidence", Reason: "File holds chunk"},
				DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "text", TargetNameProperty: "text",
					BusinessIdentity: &interfaces.DraftBusinessIdentityEvidence{ResourceID: "private-resource", Rows: 3, Distinct: 3}},
			}},
		}}
}

// askCapture runs one Ask AI call against the draft given and returns the Agent request it got.
type askCapture struct {
	request *interfaces.BknAgentChatRequest
	input   askInput
}

func askService(t *testing.T, draft *interfaces.Draft, reply string, got *askCapture) *draftService {
	t.Helper()
	ctrl := gomock.NewController(t)
	ba := pmock.NewMockBknAgentAccess(ctrl)
	ps := pmock.NewMockProjectService(ctrl)
	da := pmock.NewMockDraftAccess(ctrl)
	ps.EXPECT().GetProject(gomock.Any(), gomock.Any(), "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller}}, nil)
	da.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
	ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest, on func(interfaces.BknAgentChatEvent)) error {
		got.request = req
		if err := json.Unmarshal([]byte(req.Message), &got.input); err != nil {
			t.Fatal(err)
		}
		return answered(reply, "ok")(ctx, account, req, on)
	})
	return &draftService{setting: testSetting, ba: ba, ps: ps, da: da}
}

func endIDs(ends []askObjectType) []string {
	ids := []string{}
	for _, end := range ends {
		ids = append(ids, end.ID)
	}
	return ids
}

// The Agent reads a document relation with its document context and mapping, never the private
// business identity count. Asking about a relation that needs repair offers other object types as
// ends, but no evidence_* object type.
func TestAskDocumentRelationCarriesDocumentContextAndOffersNoEvidenceEnds(t *testing.T) {
	var got askCapture
	service := askService(t, evidenceDraft(), `{"revisions":[]}`, &got)
	if _, err := service.AskRelationType(context.Background(), token, "p", "contains", interfaces.DraftRelationAskRequest{Question: "Is this right?"}, caller, nil); err != nil {
		t.Fatal(err)
	}
	relation := got.input.RelationType
	document, _ := relation.Document.(map[string]any)
	if document["role"] != "evidence_contains" || document["layer"] != "evidence" || document["reason"] != "File holds chunk" {
		t.Fatal("document context missing from the Agent input", relation.Document)
	}
	if relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "same_document" || relation.DocumentMapping.SourceNameProperty != "text" {
		t.Fatal("document mapping missing from the Agent input", relation.DocumentMapping)
	}
	if relation.DocumentMapping.BusinessIdentity != nil || strings.Contains(got.request.Message, "private-resource") {
		t.Fatal("private business identity reached the Agent")
	}
	if ids := endIDs(got.input.Ends); !slices.Equal(ids, []string{"file_doc", "chunk", "party"}) {
		t.Fatal("offered ends", ids)
	}
	// A document relation other than a native relation may only be renamed or recommented.
	if !slices.Equal(got.input.Editable, []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT}) {
		t.Fatal("editable", got.input.Editable)
	}
}

// The unsaved form of a document relation gives the Agent its cardinality and document mapping,
// without defining a table join.
func TestAskDocumentRelationReadsTheUnsavedForm(t *testing.T) {
	var got askCapture
	service := askService(t, evidenceDraft(), `{"revisions":[]}`, &got)
	working := &interfaces.DraftRelationTypeSave{Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_MANY,
		DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "text", IdentityProperty: "text"}}
	if _, err := service.AskRelationType(context.Background(), token, "p", "contains", interfaces.DraftRelationAskRequest{Question: "Is this right?", Working: working}, caller, nil); err != nil {
		t.Fatal(err)
	}
	relation := got.input.RelationType
	if relation.Cardinality != interfaces.DRAFT_CARDINALITY_MANY_TO_MANY || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" || relation.DocumentMapping.MentionProperty != "text" {
		t.Fatal("unsaved document form not asked about", relation.Cardinality, relation.DocumentMapping)
	}
}

// Asking about a document object type takes the unsaved form's display key as given, even when
// the form clears it; and every revision schema the Agent gets carries a document_mapping.
func TestAskDocumentObjectReadsTheUnsavedFormAndSchemaCarriesDocumentMapping(t *testing.T) {
	var got askCapture
	service := askService(t, evidenceDraft(), `{"revisions":[]}`, &got)
	working := &interfaces.DraftObjectTypeSave{Name: "Chunk", DisplayKey: " "}
	if _, err := service.AskObjectType(context.Background(), token, "p", "chunk", interfaces.DraftAskRequest{Question: "Is this right?", Working: working}, caller, nil); err != nil {
		t.Fatal(err)
	}
	if got.input.ObjectType.Name != "Chunk" || got.input.ObjectType.DisplayKey != "" {
		t.Fatal("unsaved display key not asked about", got.input.ObjectType.DisplayKey)
	}
	revision := got.request.ResponseFormat["properties"].(map[string]any)["revisions"].(map[string]any)["items"].(map[string]any)
	mapping, ok := revision["properties"].(map[string]any)["document_mapping"].(map[string]any)
	if !ok || !slices.Equal(mapping["required"].([]string), []string{"mode"}) {
		t.Fatal("revision schema lacks document_mapping", revision["properties"])
	}
	modes := mapping["properties"].(map[string]any)["mode"].(map[string]any)["enum"].([]string)
	if !slices.Equal(modes, []string{"linking", "same_document"}) {
		t.Fatal("document_mapping modes", modes)
	}
}

// An identity revision of a document entity naming a managed, read-only field is dropped, though
// the field could be a key of a table object type.
func TestAskDocumentEntityDropsIdentityOfManagedField(t *testing.T) {
	kb.ResetForTest()
	entitlement.ResetForTest()
	t.Cleanup(func() { kb.ResetForTest(); entitlement.ResetForTest() })
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	kb.Register(kb.Providers{Source: &documentSaveSource{}, Concepts: kb.UnavailableConcepts{}})
	draft := editableDocumentDraft(t)
	managed := ""
	for _, p := range draft.Items.ObjectTypes[0].Properties {
		if p.ReadOnly && keyTypes[p.Type] {
			managed = p.Name
		}
	}
	if managed == "" {
		t.Fatal("fixture has no managed key field")
	}
	var got askCapture
	service := askService(t, draft, `{"revisions":[{"field":"primary_keys","properties":["`+managed+`"],"reason":"Use the managed id"}]}`, &got)
	useDocumentResources(t, documentSaveSelection{})
	answer, err := service.AskObjectType(context.Background(), "Bearer synthetic", "p", "order_doc", interfaces.DraftAskRequest{Question: "Which identity?"}, caller, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Revisions) != 0 || len(answer.Dropped) != 1 || answer.Dropped[0].Reason != askDroppedInvalidIdentity {
		t.Fatal("managed identity revision kept", answer.Revisions, answer.Dropped)
	}
}

// Reading a draft shows the state of each dual-source plan: proposed until confirmed, stale once
// confirmed but no longer ready.
func TestGetDraftShowsDualSourcePlanState(t *testing.T) {
	draft := evidenceDraft()
	draft.Items.ObjectTypes[3].Document.DualSource = &interfaces.DraftDualSourcePlan{}
	draft.Items.ObjectTypes[0].Document.DualSource = &interfaces.DraftDualSourcePlan{ConfirmedAt: 1}
	ctrl := gomock.NewController(t)
	ps := pmock.NewMockProjectService(ctrl)
	da := pmock.NewMockDraftAccess(ctrl)
	ps.EXPECT().GetProject(gomock.Any(), token, "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p"}}, nil)
	da.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
	result, err := (&draftService{ps: ps, da: da}).GetDraft(context.Background(), token, "p")
	if err != nil {
		t.Fatal(err)
	}
	if state := result.Items.ObjectTypes[3].Document.DualSource.State; state != "proposed" {
		t.Fatal("unconfirmed plan state", state)
	}
	if state := result.Items.ObjectTypes[0].Document.DualSource.State; state != "stale" {
		t.Fatal("confirmed plan not ready state", state)
	}
}

// Reading a draft with Dataset plans reports the progress of the plan of the current source
// selection.
func TestGetDraftReportsDatasetProgressOfCurrentSelection(t *testing.T) {
	service, access, _, _, _ := preparedDraftFixture(t)
	access.current.UpdateTime = time.Now().UnixMilli()
	plan, err := kbdataset.NewPlan(access.current, "generation", "catalog", access.owner)
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "preparing"
	plan.Datasets[0].Ready = true
	access.current.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	result, err := service.GetDraft(context.Background(), "Bearer operator", "p")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.DatasetPreparation; got == nil || got.State != "preparing" || got.Ready != 1 || got.Total != 1 {
		t.Fatal("dataset progress", got)
	}
}

// Confirming a draft whose document item holds a confirmed field plan that is no longer ready is
// refused before any Dataset is prepared or anything is written.
func TestConfirmDraftRefusesUnreadyDualSourcePlan(t *testing.T) {
	service, access, _, storage, _ := preparedDraftFixture(t)
	access.current.Items.ObjectTypes[0].Document.DualSource = &interfaces.DraftDualSourcePlan{ConfirmedAt: 1}
	ctrl := gomock.NewController(t)
	rqa := pmock.NewMockRequirementAccess(ctrl)
	rqa.EXPECT().GetRequirement(gomock.Any(), "p").Return(nil, false, nil)
	// Nothing past the refusal may run: these mocks expect no call.
	service.rqa, service.ls, service.ira = rqa, pmock.NewMockLineageService(ctrl), pmock.NewMockIDRegistryAccess(ctrl)
	_, err := service.ConfirmDraft(context.Background(), "Bearer operator", "p", interfaces.DraftConfirm{UpdateTime: 10}, access.owner)
	var problem *httperrors.HTTPError
	if !errors.As(err, &problem) || problem.HTTPCode != 409 || problem.ErrorCode != perrors.BknProject_Draft_DualSourceNotConfirmed {
		t.Fatal("unready field plan confirmed", err)
	}
	if len(storage.calls) != 0 {
		t.Fatal("datasets prepared before refusal")
	}
}

// agentHooks is the built-in hooks with an Agent of their own for questions about document items.
type agentHooks struct{ docdraft.Hooks }

func (agentHooks) AskAgentID(draft *interfaces.Draft) string {
	if draft != nil && len(draft.Items.ObjectTypes) > 0 && draft.Items.ObjectTypes[0].Document != nil {
		return "edition-document-ask"
	}
	return ""
}

// Questions about a draft go to the Agent the hooks name, and to the one of the open core when
// they name none.
func TestAskGoesToTheAgentTheHooksName(t *testing.T) {
	projecthooks.Register(projecthooks.Hooks{Drafts: agentHooks{}})
	t.Cleanup(func() { projecthooks.Register(projecthooks.Hooks{Drafts: docdraft.Hooks{}}) })
	var got askCapture
	service := askService(t, evidenceDraft(), `{"revisions":[]}`, &got)
	if _, err := service.AskRelationType(context.Background(), token, "p", "contains", interfaces.DraftRelationAskRequest{Question: "Is this right?"}, caller, nil); err != nil {
		t.Fatal(err)
	}
	if got.request.AgentID != "edition-document-ask" {
		t.Fatal("agent", got.request.AgentID)
	}
	projecthooks.Register(projecthooks.Hooks{Drafts: docdraft.Hooks{}})
	got = askCapture{}
	service = askService(t, evidenceDraft(), `{"revisions":[]}`, &got)
	if _, err := service.AskRelationType(context.Background(), token, "p", "contains", interfaces.DraftRelationAskRequest{Question: "Is this right?"}, caller, nil); err != nil {
		t.Fatal(err)
	}
	if got.request.AgentID != askAgentID {
		t.Fatal("agent", got.request.AgentID)
	}
}
