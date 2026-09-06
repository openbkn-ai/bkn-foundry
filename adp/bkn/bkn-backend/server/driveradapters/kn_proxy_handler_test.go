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
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestResolveKNProxyBindingEndpointUsesServerMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := gomock.NewController(t)
	service := bmock.NewMockKNService(ctrl)
	handler := &restHandler{kns: service}
	engine := gin.New()
	engine.POST("/api/bkn-backend/in/v1/knowledge-networks/:kn_id/proxy-account/resolve", handler.ResolveKNProxyBinding)

	binding := interfaces.KNProxyBinding{
		ChildType: interfaces.MODULE_TYPE_OBJECT_TYPE, ChildID: "ot-1",
		TargetType: "resource", TargetID: "resource-1", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
	}
	service.EXPECT().ResolveKNProxyBinding(gomock.Any(), "kn-1", binding).
		DoAndReturn(func(ctx context.Context, _ string, _ interfaces.KNProxyBinding) (*interfaces.KNProxyAccount, error) {
			account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			if account.ID != "caller-1" || account.Type != "user" {
				t.Fatalf("unexpected caller context: %#v", account)
			}
			return &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1"}, nil
		})

	req := httptest.NewRequest(http.MethodPost,
		"/api/bkn-backend/in/v1/knowledge-networks/kn-1/proxy-account/resolve",
		strings.NewReader(`{"child_type":"object_type","child_id":"ot-1","target_type":"resource","target_id":"resource-1","operation":"query_data"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "caller-1")
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
