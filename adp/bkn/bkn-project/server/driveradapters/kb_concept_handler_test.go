// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
)

type conceptHTTPStore struct {
	interfaces.KBResourceAccess
	interfaces.DraftAccess
	saved *interfaces.Draft
}

func (s *conceptHTTPStore) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	return &interfaces.KBResourceState{Current: &interfaces.KBSelection{ID: "lock", State: "active", Revision: 7, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", ChunkCount: 1, SampleRole: "debug"}}}}, nil
}
func (s *conceptHTTPStore) GetDraft(context.Context, string) (*interfaces.Draft, bool, error) {
	return nil, false, nil
}
func (s *conceptHTTPStore) CommitDocumentDraft(_ context.Context, d *interfaces.Draft, g interfaces.DocumentDraftGuard) (bool, error) {
	s.saved = d
	return true, nil
}

type conceptHTTPSource struct{ kb.UnavailableSource }

func (s conceptHTTPSource) SummarizeFiles(context.Context, kb.SourceScope, kb.FileQuery) (kb.FileSummary, error) {
	return kb.FileSummary{}, nil
}
func (s conceptHTTPSource) ReadSampleChunks(context.Context, []kb.SampleVersion, kb.PageQuery) (kb.SamplePage, error) {
	return kb.SamplePage{Entries: []kb.SampleChunk{{SampleVersion: kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "41", ContentVersionID: "8"}}, ChunkID: "1", Text: "Order O-1", Located: "exact", Spans: []kb.Span{{BlockID: "b1", Start: 0, End: 9}}}}}, nil
}

type conceptHTTPProvider struct{}

func (conceptHTTPProvider) Extract(_ context.Context, r kb.ConceptRequest) (kb.ConceptProposal, error) {
	q := r.Samples[0].Citation
	proof := interfaces.DraftSourceCitation{KnowledgeBaseID: q.KnowledgeBaseID, FileID: q.FileID, ContentVersionID: q.ContentVersionID, BlockID: q.BlockID, Start: q.Start, End: q.End, Quote: q.Quote}
	return kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{{ID: "order_doc", Name: "Order", PrimaryKeys: []string{"number"}, Document: &interfaces.DraftDocumentModel{Role: "entity", Reason: "An explicit order number", Citations: []interfaces.DraftSourceCitation{proof}}, Properties: []*interfaces.DraftProperty{{Name: "number", DisplayName: "Number", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit order number", Examples: []interfaces.DraftSourceCitation{proof}}}}}}}, nil
}
func TestConceptHTTPUsesExistingUserAndCapabilityBoundary(t *testing.T) {
	for _, scenario := range []string{"success", "community", "app", "not_owner", "invalid", "too_large"} {
		t.Run(scenario, func(t *testing.T) {
			reset := setGinMode()
			defer reset()
			kb.ResetForTest()
			entitlement.ResetForTest()
			defer kb.ResetForTest()
			defer entitlement.ResetForTest()
			edition := licverify.EditionProfessional
			if scenario == "community" {
				edition = licverify.EditionCommunity
			}
			entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: edition} }))
			kb.Register(kb.Providers{Source: conceptHTTPSource{}, Concepts: conceptHTTPProvider{}})
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			as := pmock.NewMockAuthService(ctrl)
			ps := pmock.NewMockProjectService(ctrl)
			visitor := hydra.Visitor{ID: "u", Type: hydra.VisitorType_User, TokenID: "Bearer synthetic"}
			if scenario == "app" {
				visitor.Type = hydra.VisitorType_App
			}
			as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(visitor, nil)
			if scenario == "success" || scenario == "not_owner" {
				owner := interfaces.AccountInfo{ID: "u", Type: "user"}
				if scenario == "not_owner" {
					owner.ID = "other"
				}
				ps.EXPECT().GetProject(gomock.Any(), "Bearer synthetic", "p").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner, NamingLanguage: "en-US"}}, nil)
			}
			store := &conceptHTTPStore{}
			r := &restHandler{as: as, ps: ps, kbra: store, da: store}
			engine := gin.New()
			engine.Use(r.LanguageMiddleware())
			r.registerKnowledgeBaseResources(engine)
			body := `{"selection_id":"lock","selection_revision":7,"draft_update_time":0}`
			if scenario == "invalid" {
				body = `{}`
			}
			if scenario == "too_large" {
				body = strings.Repeat(" ", 4097) + body
			}
			req := httptest.NewRequest(http.MethodPost, "/api/bkn-project/v1/projects/p/kb/concepts:extract", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Language", "en-US")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			expected := 200
			switch scenario {
			case "community":
				expected = 404
			case "app", "not_owner":
				expected = 403
			case "invalid", "too_large":
				expected = 400
			}
			if w.Code != expected {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if (store.saved != nil) != (scenario == "success") {
				t.Fatal("unauthorized or invalid request wrote a draft")
			}
			if scenario == "success" && (!strings.Contains(w.Body.String(), `"order_doc"`) || strings.Contains(w.Body.String(), `"quote":"Order O-1"`)) {
				t.Fatal("candidate response or source policy changed")
			}
		})
	}
}
