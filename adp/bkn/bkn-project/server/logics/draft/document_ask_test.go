// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
)

func TestDocumentInstructionRevisionsAreProposalsWithProtectedFields(t *testing.T) {
	d := editableDocumentDraft(t)
	o := d.Items.ObjectTypes[0]
	instruction := "Read the contract's explicit number only"
	out := askRevisionOutput{Field: interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY, Property: &interfaces.DraftAskPropertyRevision{Name: "number", ExtractionInstruction: &instruction}}
	revision, why := objectTypeRevision(context.Background(), o, out)
	if revision == nil || why != "" || revision.Value.(interfaces.DraftAskPropertyRevision).ExtractionInstruction == nil {
		t.Fatal("valid instruction proposal rejected", why)
	}
	if o.Properties[0].Extraction.Instruction == instruction {
		t.Fatal("AI modified draft")
	}
	out.Property.Name = "document_id"
	if revision, _ = objectTypeRevision(context.Background(), o, out); revision != nil {
		t.Fatal("managed source field editable")
	}
	out.Property.Name = "number"
	large := strings.Repeat("x", 1001)
	out.Property.ExtractionInstruction = &large
	if revision, _ = objectTypeRevision(context.Background(), o, out); revision != nil {
		t.Fatal("oversized instruction editable")
	}
	o.Document = nil
	out.Property.ExtractionInstruction = &instruction
	if revision, _ = objectTypeRevision(context.Background(), o, out); revision != nil {
		t.Fatal("table property accepted document instruction")
	}
}
func TestDocumentAskContextExplainsRoleAndDoesNotInventTableCounts(t *testing.T) {
	d := editableDocumentDraft(t)
	o := d.Items.ObjectTypes[0]
	asked := askObjectTypeOf(o)
	raw, err := json.Marshal(asked)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["identity_evidence"]; ok {
		t.Fatal("document model described with table counts")
	}
	if _, ok := body["table"]; ok {
		t.Fatal("document model described as a table")
	}
	if !strings.Contains(string(raw), "extraction_instruction") || !strings.Contains(string(raw), "read_only") || !strings.Contains(string(raw), `"role":"entity"`) {
		t.Fatal("missing grounded modeling context", string(raw))
	}
	instruction := "Read the whole printed number"
	working := &interfaces.DraftObjectTypeSave{Name: o.Name, PrimaryKeys: o.PrimaryKeys, DataProperties: []interfaces.DraftDataPropertySave{{Name: "number", DisplayName: "Number", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: instruction, Examples: o.Properties[0].Extraction.Examples}}}}
	changed := workingObjectType(o, working)
	if changed.Properties[0].Extraction.Instruction != instruction || o.Properties[0].Extraction.Instruction == instruction {
		t.Fatal("unsaved instruction missing or draft changed")
	}
}

func TestDocumentAskUsesVerifiedDebugExamplesWithoutChangingStoredProof(t *testing.T) {
	kb.ResetForTest()
	entitlement.ResetForTest()
	defer kb.ResetForTest()
	defer entitlement.ResetForTest()
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	source := &documentSaveSource{}
	kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}})
	draft := editableDocumentDraft(t)
	original := draft.Items.ObjectTypes[0]
	before, _ := json.Marshal(original)
	service := &draftService{}
	useDocumentResources(t, documentSaveSelection{})
	_, asked, _, err := service.documentAskObject(context.Background(), "Bearer synthetic", draft, original, workingObjectType(original, nil))
	if err != nil || len(asked.Properties[0].Examples) != 1 || asked.Properties[0].Examples[0] != "Order O-1" || source.reads != 1 {
		t.Fatal("native debug examples missing", err)
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("assistant context changed stored proof")
	}
	bad := workingObjectType(original, nil)
	bad.Properties[0].Extraction = &interfaces.DraftExtractionField{Instruction: "Read the number", Examples: []interfaces.DraftSourceCitation{{KnowledgeBaseID: "1", FileID: "42", ContentVersionID: "9", BlockID: "acceptance", Start: 0, End: 3}}}
	if _, _, _, err = service.documentAskObject(context.Background(), "Bearer synthetic", draft, original, bad); err == nil {
		t.Fatal("unverified example entered assistant context")
	}
}
func TestDocumentRelationAssistantChecksNativeMappingsWithoutTableCounts(t *testing.T) {
	draft := editableDocumentDraft(t)
	raw, _ := json.Marshal(draft.Items.ObjectTypes[0])
	target := &interfaces.DraftObjectType{}
	json.Unmarshal(raw, target)
	target.ID = "invoice_doc"
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, target)
	for _, object := range draft.Items.ObjectTypes {
		object.Document.Role, object.Document.Layer = "file", "fact"
		object.PrimaryKeys = []string{"_id"}
	}
	state, _ := documentSaveSelection{}.Read(context.Background(), "p")
	input, err := kbconcept.LoadSamples(context.Background(), &documentSaveSource{}, state.Current, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	original := &interfaces.DraftRelationType{ID: "order_invoice", Name: "Related invoice", Source: "order_doc", Target: "invoice_doc", Origin: interfaces.DRAFT_ORIGIN_MANUAL, Cardinality: "many_to_one", Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "number", TargetProperty: "_id"}}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "_id"}, Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: "Explicit number", Citations: draft.Items.ObjectTypes[0].Document.Citations, SelectionID: "lock", SelectionRevision: 7}}
	draft.Items.RelationTypes = []*interfaces.DraftRelationType{original}
	before, _ := json.Marshal(original)
	out := askRevisionOutput{Field: interfaces.DRAFT_ASK_REVISION_ENDS, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "same_document", SourceNameProperty: "number", TargetNameProperty: "number"}, Cardinality: "one_to_many"}
	revision, reason := documentRelationRevision(context.Background(), input, draft.Items, original, out)
	if revision == nil || reason != "" {
		t.Fatal("valid document relation revision rejected", reason)
	}
	result := revision.Value.(interfaces.DraftAskEndsRevision)
	if result.DocumentMapping.Mode != "same_document" || result.Cardinality != "one_to_many" {
		t.Fatal("document rule lost")
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("AI wrote relation")
	}
	out.DocumentMapping.SourceNameProperty = "missing"
	if revision, _ = documentRelationRevision(context.Background(), input, draft.Items, original, out); revision != nil {
		t.Fatal("invalid document mapping accepted")
	}
}

func TestNewDocumentRelationAssistantDoesNotQueryTablesOrSave(t *testing.T) {
	kb.ResetForTest()
	entitlement.ResetForTest()
	defer kb.ResetForTest()
	defer entitlement.ResetForTest()
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	source := &documentSaveSource{}
	kb.Register(kb.Providers{Source: source, Concepts: kb.UnavailableConcepts{}})
	ctrl := gomock.NewController(t)
	ba := pmock.NewMockBknAgentAccess(ctrl)
	draft := editableDocumentDraft(t)
	raw, _ := json.Marshal(draft.Items.ObjectTypes[0])
	target := &interfaces.DraftObjectType{}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
	target.ID = "invoice_doc"
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, target)
	before, _ := json.Marshal(draft)
	setting := testSetting
	setting.QueryTimeout = 60
	service := &draftService{setting: setting, ba: ba,
		va: pmock.NewMockVegaAccess(ctrl), da: pmock.NewMockDraftAccess(ctrl)}
	useDocumentResources(t, documentSaveSelection{})
	ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest, on func(interfaces.BknAgentChatEvent)) error {
			var in askInput
			if err := json.Unmarshal([]byte(req.Message), &in); err != nil {
				t.Fatal(err)
			}
			if in.RelationType.ID != "" || len(in.ChosenEnds) != 2 || len(in.Ends) != 2 || strings.Contains(req.Message, "identity_evidence") {
				t.Fatal("new document relation context is not grounded")
			}
			return answered(`{"revisions":[{"field":"ends","document_mapping":{"mode":"linking","mention_property":"number","identity_property":"number"},"cardinality":"one_to_many","reason":"Use the printed business number"}]}`, "Use the explicit number.")(ctx, account, req, on)
		})
	result, err := service.askNewDocumentRelation(context.Background(), "Bearer synthetic", draft, askInput{}, interfaces.DraftRelationAskRequest{Working: &interfaces.DraftRelationTypeSave{SourceObjectTypeID: "order_doc", TargetObjectTypeID: "invoice_doc", DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}, Cardinality: "many_to_one"}}, caller, nil)
	if err != nil || result == nil || len(result.Revisions) != 1 {
		t.Fatal("document proposal failed", err, result)
	}
	ends := result.Revisions[0].Value.(interfaces.DraftAskEndsRevision)
	if ends.DocumentMapping == nil || ends.DocumentMapping.Mode != "linking" || ends.Cardinality != "one_to_many" || len(ends.MappingRules) != 0 {
		t.Fatal("document proposal became a table mapping")
	}
	after, _ := json.Marshal(draft)
	if string(before) != string(after) {
		t.Fatal("assistant wrote draft")
	}
}

func TestBlockedRelationshipAssistantProposesStrictRepairsWithoutWriting(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "grounded repair", false: "invalid suggestion"}[valid], func(t *testing.T) {
			kb.ResetForTest()
			entitlement.ResetForTest()
			defer kb.ResetForTest()
			defer entitlement.ResetForTest()
			entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
			kb.Register(kb.Providers{Source: &documentSaveSource{}, Concepts: kb.UnavailableConcepts{}})
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			ba := pmock.NewMockBknAgentAccess(ctrl)
			ps := pmock.NewMockProjectService(ctrl)
			da := pmock.NewMockDraftAccess(ctrl)
			draft := editableDocumentDraft(t)
			source := draft.Items.ObjectTypes[0]
			copyObject := func(id string) *interfaces.DraftObjectType {
				raw, _ := json.Marshal(source)
				var object interfaces.DraftObjectType
				_ = json.Unmarshal(raw, &object)
				object.ID = id
				return &object
			}
			local := copyObject("local_claim")
			local.Document.Role = "in_document"
			local.Document.Layer = "fact"
			local.PrimaryKeys = []string{"_id"}
			alternate := copyObject("current_doc")
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, local, alternate)
			relation := &interfaces.DraftRelationType{ID: "reference", Name: "References", Source: source.ID, Target: local.ID, Cardinality: "many_to_one", DocumentRepair: "target_identity", DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}, Document: &interfaces.DraftDocumentModel{Role: "relation", Layer: "fact", Reason: "Explicit source number", Citations: source.Document.Citations, SelectionID: "lock", SelectionRevision: 7}}
			draft.Items.RelationTypes = []*interfaces.DraftRelationType{relation}
			before, _ := json.Marshal(draft)
			ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: caller}}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p").Return(draft, true, nil)
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, request *interfaces.BknAgentChatRequest, on func(interfaces.BknAgentChatEvent)) error {
				var input askInput
				if json.Unmarshal([]byte(request.Message), &input) != nil || input.RelationType.DocumentRepair != "target_identity" || len(input.Ends) != 3 {
					t.Fatal("repair context missing grounded alternatives")
				}
				property := "number"
				if !valid {
					property = "unknown"
				}
				output := `{"revisions":[{"field":"ends","source_object_type_id":"order_doc","target_object_type_id":"current_doc","document_mapping":{"mode":"linking","mention_property":"number","identity_property":"` + property + `"},"cardinality":"many_to_one","reason":"Use the confirmed global business identity"}]}`
				return answered(output, "Review this relationship.")(ctx, account, request, on)
			})
			service := &draftService{setting: testSetting, ba: ba, ps: ps, da: da}
			useDocumentResources(t, documentSaveSelection{})
			got, err := service.AskRelationType(context.Background(), "Bearer synthetic", "p", relation.ID, interfaces.DraftRelationAskRequest{Question: "Help repair this relationship"}, caller, nil)
			if err != nil || got == nil {
				t.Fatal("blocked advice failed", err)
			}
			if valid && len(got.Revisions) != 1 {
				t.Fatal("valid repair suggestion dropped")
			}
			if !valid && (len(got.Revisions) != 0 || len(got.Dropped) != 1) {
				t.Fatal("invalid suggestion reached editor")
			}
			after, _ := json.Marshal(draft)
			if string(before) != string(after) {
				t.Fatal("assistant modified saved draft or cleared its repair block")
			}
		})
	}
}
