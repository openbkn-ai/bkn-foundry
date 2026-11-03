// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package driveradapters

import (
	"context"
	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbrefresh"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
	"net/http/httptest"
	"strings"
	"testing"
)

type refreshHandlerReleases struct{ interfaces.ReleaseAccess }

func (*refreshHandlerReleases) GetLatestSwitch(context.Context, string) (*interfaces.Switch, bool, error) {
	return &interfaces.Switch{LineageID: "lineage", ToNetworkID: "release"}, true, nil
}
func (*refreshHandlerReleases) GetReleaseByNetworkID(context.Context, string) (*interfaces.Release, bool, error) {
	return &interfaces.Release{NetworkID: "release", LineageID: "lineage", ProjectID: "p", Publisher: interfaces.AccountInfo{ID: "publisher", Type: "user"}}, true, nil
}

type refreshHandlerPublications struct{ interfaces.KBPublicationAccess }

func (*refreshHandlerPublications) Get(context.Context, string) (*interfaces.KBPublication, error) {
	return &interfaces.KBPublication{NetworkID: "release", LineageID: "lineage", ProjectID: "p", Publisher: interfaces.AccountInfo{ID: "publisher", Type: "user"}, State: "active", GenerationID: "generation", FullBuildID: "full", Revision: 1}, nil
}

type refreshHandlerJournal struct {
	interfaces.KBRefreshAccess
	reads int
}

func (j *refreshHandlerJournal) Get(context.Context, string) (*interfaces.KBRefreshRecord, error) {
	j.reads++
	return &interfaces.KBRefreshRecord{LineageID: "lineage", NetworkID: "release", Revision: 4, State: "attention", Reason: "batch_review_required", Pending: &interfaces.KBRefreshBatch{Build: &interfaces.KBBuild{ID: "private-task", ModelID: "private-model", Files: []interfaces.KBFileBuild{{AgentTaskID: "private-agent-task"}}}, Holds: []interfaces.KBHoldGroup{{ID: "private-hold"}}, Estimate: &interfaces.KBRefreshEstimate{Reasons: []string{"duration_unknown"}, ChangedFiles: 1}}}, nil
}
func TestRefreshStatusIsReadOnlyAuthorizedAndDoesNotExposePrivateJournal(t *testing.T) {
	cleanup := setGinMode()
	defer cleanup()
	kb.ResetForTest()
	entitlement.ResetForTest()
	defer kb.ResetForTest()
	defer entitlement.ResetForTest()
	entitlement.SetGate(entitlement.GateFunc(func() entitlement.Snapshot { return entitlement.Snapshot{Edition: licverify.EditionProfessional} }))
	kb.Register(kb.Providers{Source: kb.UnavailableSource{}, Concepts: kb.UnavailableConcepts{}})
	ctrl := gomock.NewController(t)
	auth := pmock.NewMockAuthService(ctrl)
	backend := pmock.NewMockBknBackendAccess(ctrl)
	oldBackend := logics.BBA
	logics.BBA = backend
	defer func() { logics.BBA = oldBackend }()
	visitor := hydra.Visitor{ID: "viewer", Type: hydra.VisitorType_User, TokenID: "Bearer synthetic-operator"}
	auth.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(context.Context, *gin.Context) (hydra.Visitor, error) { return visitor, nil })
	journal := &refreshHandlerJournal{}
	handler := &restHandler{as: auth, refreshFactory: func() *kbrefresh.Service {
		return &kbrefresh.Service{Journal: journal, Releases: &refreshHandlerReleases{}, Publications: &refreshHandlerPublications{}}
	}}
	engine := gin.New()
	engine.Use(handler.LanguageMiddleware())
	handler.registerKnowledgeBaseRefresh(engine)
	send := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/bkn-project/v1/lineages/lineage/kb-refresh", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept-Language", "en-US")
		engine.ServeHTTP(w, r)
		return w
	}
	backend.EXPECT().GetKN(gomock.Any(), visitor.TokenID, "release").Return(&interfaces.KnowledgeNetwork{ID: "release", Name: "Contracts", Operations: []string{"view_detail"}}, nil).Times(2)
	response := send("GET", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"can_manage":false`) || journal.reads != 1 {
		t.Fatalf("status denied or mutated work: %d %s", response.Code, response.Body.String())
	}
	for _, private := range []string{"private-task", "private-model", "private-agent-task", "private-hold", "synthetic-operator", "model_fingerprint", "runtime_model"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("private journal field exposed: %s", private)
		}
	}
	if response := send("POST", `{"action":"release","revision":4}`); response.Code != 403 || journal.reads != 1 {
		t.Fatal("view-only caller reached review command")
	}
	visitor.Type = hydra.VisitorType_App
	if response := send("GET", ""); response.Code != 403 {
		t.Fatal("service account entered human management path")
	}
}
