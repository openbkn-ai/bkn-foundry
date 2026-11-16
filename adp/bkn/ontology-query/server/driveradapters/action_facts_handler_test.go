// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

func TestActionFactCheckAcceptsOnlyTrustedApplication(t *testing.T) {
	t.Setenv("GOVERNANCE_FACTS_CLIENT_ID", "governance")
	for _, sample := range []struct {
		name    string
		visitor hydra.Visitor
		trusted string
		status  int
	}{
		{"trusted app", hydra.Visitor{ID: "governance", ClientID: "governance", Type: hydra.VisitorType_App}, "governance", http.StatusNoContent},
		{"missing initiator", hydra.Visitor{ID: "governance", ClientID: "governance", Type: hydra.VisitorType_App}, "governance", http.StatusBadRequest},
		{"other app", hydra.Visitor{ID: "other", ClientID: "other", Type: hydra.VisitorType_App}, "governance", http.StatusForbidden},
		{"user", hydra.Visitor{ID: "alice", ClientID: "governance", Type: hydra.VisitorType_User}, "governance", http.StatusForbidden},
		{"disabled", hydra.Visitor{ID: "governance", ClientID: "governance", Type: hydra.VisitorType_App}, "", http.StatusForbidden},
	} {
		t.Run(sample.name, func(t *testing.T) {
			t.Setenv("GOVERNANCE_FACTS_CLIENT_ID", sample.trusted)
			ctrl := gomock.NewController(t)
			auth := omock.NewMockAuthService(ctrl)
			scheduler := omock.NewMockActionSchedulerService(ctrl)
			auth.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(sample.visitor, nil)
			if sample.status == http.StatusNoContent {
				scheduler.EXPECT().CheckActionFacts(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, check *interfaces.ActionFactCheck) error {
					account := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
					if account.ID != "alice" || account.Type != string(hydra.VisitorType_User) || check.Initiator != "alice" {
						t.Errorf("fact check did not use saved initiator permissions: %+v", account)
					}
					if check.KNID != "kn" || check.ActionTypeID != "pay" {
						t.Errorf("route IDs were not authoritative: %+v", check)
					}
					return nil
				})
			}
			handler := &restHandler{as: auth, ass: scheduler}
			engine := gin.New()
			engine.POST("/knowledge-networks/:kn_id/action-types/:at_id/execute/facts/check", handler.CheckActionFactsByIn)
			initiator := "alice"
			if sample.name == "missing initiator" {
				initiator = ""
			}
			request := httptest.NewRequest(http.MethodPost, "/knowledge-networks/kn/action-types/pay/execute/facts/check",
				strings.NewReader(`{"initiator":"`+initiator+`","definition_digest":"`+strings.Repeat("a", 64)+`","target_identity":{"id":"one"}}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != sample.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), sample.status)
			}
		})
	}
}
