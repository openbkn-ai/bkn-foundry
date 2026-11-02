// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/licverify"
	"go.uber.org/mock/gomock"
)

type systemLinkHTTPDataset struct {
	kb.Datasets
	kb.DatasetFileSnapshot
	kb.DatasetFileRecords
}
type systemLinkHTTPQueue struct{ interfaces.KBBuildQueue }
type systemLinkHTTPVega struct{ interfaces.VegaAccess }

func TestSystemLinksHTTPRejectsForgedWritesAndNonInteractiveIdentities(t *testing.T) {
	for _, scenario := range []string{"community", "app", "forged_rows", "forged_scope", "trailing_body", "hidden_project"} {
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
			kb.Register(kb.Providers{Source: kb.UnavailableSource{}, Concepts: kb.UnavailableConcepts{}, Datasets: &systemLinkHTTPDataset{}})
			oldVega := logics.VA
			logics.VA = &systemLinkHTTPVega{}
			defer func() { logics.VA = oldVega }()
			old := logics.KBBA
			logics.KBBA = &systemLinkHTTPQueue{}
			defer func() { logics.KBBA = old }()
			ctrl := gomock.NewController(t)
			auth, projects := pmock.NewMockAuthService(ctrl), pmock.NewMockProjectService(ctrl)
			visitor := hydra.Visitor{ID: "owner", Type: hydra.VisitorType_User, TokenID: "Bearer private-operator"}
			if scenario == "app" {
				visitor.Type = hydra.VisitorType_App
			}
			auth.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(visitor, nil)
			handler := &restHandler{as: auth, ps: projects, da: pmock.NewMockDraftAccess(ctrl), kbra: pmock.NewMockKBResourceAccess(ctrl)}
			engine := gin.New()
			engine.Use(handler.LanguageMiddleware())
			handler.registerKBBuild(engine.Group("/api/bkn-project/v1/projects/:project_id/kb"))
			method := http.MethodPost
			body := `{"operation_id":"b4dd7499-c566-4b4e-a6be-07cdf1b08ffd","signature":"` + strings.Repeat("a", 64) + `"}`
			switch scenario {
			case "forged_rows":
				body = strings.TrimSuffix(body, "}") + `,"writes":[{"source_quote":"private evidence"}]}`
			case "forged_scope":
				body = strings.TrimSuffix(body, "}") + `,"resource_id":"foreign-system-table"}`
			case "trailing_body":
				body += `{}`
			case "hidden_project":
				method = http.MethodGet
				projects.EXPECT().GetProjectForStatus(gomock.Any(), "Bearer private-operator", "p", interfaces.AccountInfo{ID: "owner", Type: "user"}).Return(nil, false, &kb.SourceError{StatusCode: 403})
			}
			request := httptest.NewRequest(method, "/api/bkn-project/v1/projects/p/kb/builds/job/system-links", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept-Language", "en-US")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			want := http.StatusBadRequest
			if scenario == "community" {
				want = http.StatusNotFound
			}
			if scenario == "app" || scenario == "hidden_project" {
				want = http.StatusForbidden
			}
			if response.Code != want {
				t.Fatal("wrong system-link boundary", scenario, response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private evidence") || strings.Contains(response.Body.String(), "private-operator") {
				t.Fatal("input or credential leaked into error")
			}
		})
	}
}
