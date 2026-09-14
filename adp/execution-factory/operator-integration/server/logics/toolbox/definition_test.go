// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

type definitionReadAuthorizer struct {
	calls int
	err   error
}

func (a *definitionReadAuthorizer) Authorize(context.Context, interfaces.ProxyExecutionContext) error {
	a.calls++
	return a.err
}

type definitionReadAudit struct {
	events []interfaces.ProxyExecutionAuditEvent
}

func (a *definitionReadAudit) RecordProxyExecution(_ context.Context, event interfaces.ProxyExecutionAuditEvent) {
	a.events = append(a.events, event)
}

func toolDefinitionReadContext(access string) context.Context {
	return interfaces.WithProxyExecutionContext(context.Background(), interfaces.ProxyExecutionContext{
		CallerID:     "caller-1",
		CallerType:   "user",
		KnowledgeID:  "kn-1",
		ChildType:    interfaces.ProxyChildTypeAction,
		ChildID:      "action-1",
		ProxyID:      "proxy-1",
		ProxyType:    interfaces.ProxyAccountTypeApp,
		ProxyVersion: 3,
		TargetType:   interfaces.ProxyTargetTypeToolBox,
		TargetID:     "box-1",
		Operation:    interfaces.ProxyOperationExecute,
		Access:       access,
	})
}

func httpStatusOf(err error) int {
	var httpErr *oerrors.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.HTTPCode
	}
	return 0
}

// Without a route-validated definition context, or without the proxy's grant,
// the definition read stops before any table is read: strict mocks with no
// expectations prove it.
func TestGetToolDefinitionAsProxyAuthorizesBeforeReading(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		authErr    error
		wantStatus int
		wantCalls  int
	}{
		{name: "direct internal caller", ctx: context.Background(), wantStatus: http.StatusForbidden},
		{name: "execution context", ctx: toolDefinitionReadContext(""), wantStatus: http.StatusForbidden},
		{name: "grant revoked", ctx: toolDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
			authErr: interfaces.ErrProxyExecutionDenied, wantStatus: http.StatusForbidden, wantCalls: 1},
		{name: "bkn-safe unavailable", ctx: toolDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
			authErr: errors.New("connection refused"), wantStatus: http.StatusServiceUnavailable, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			authorizer := &definitionReadAuthorizer{err: test.authErr}
			svc := &ToolServiceImpl{
				Logger:          logger.DefaultLogger(),
				ToolBoxDB:       mocks.NewMockIToolboxDB(ctrl),
				ToolDB:          mocks.NewMockIToolDB(ctrl),
				MetadataService: mocks.NewMockIMetadataService(ctrl),
				ProxyAuthorizer: authorizer,
				ProxyAudit:      &definitionReadAudit{},
			}

			resp, err := svc.GetToolDefinitionAsProxy(test.ctx,
				&interfaces.GetToolDefinitionReq{BoxID: "box-1", ToolID: "tool-1"})

			if resp != nil || httpStatusOf(err) != test.wantStatus {
				t.Fatalf("GetToolDefinitionAsProxy() = %+v, %v; want HTTP %d", resp, err, test.wantStatus)
			}
			if authorizer.calls != test.wantCalls {
				t.Fatalf("authorizer calls = %d, want %d", authorizer.calls, test.wantCalls)
			}
		})
	}
}

func TestGetToolDefinitionAsProxyReturnsOnlyTheInvocationContract(t *testing.T) {
	ctrl := gomock.NewController(t)
	boxes := mocks.NewMockIToolboxDB(ctrl)
	tools := mocks.NewMockIToolDB(ctrl)
	metadataService := mocks.NewMockIMetadataService(ctrl)
	audit := &definitionReadAudit{}
	svc := &ToolServiceImpl{
		Logger:          logger.DefaultLogger(),
		ToolBoxDB:       boxes,
		ToolDB:          tools,
		MetadataService: metadataService,
		ProxyAuthorizer: &definitionReadAuthorizer{},
		ProxyAudit:      audit,
	}
	boxes.EXPECT().SelectToolBox(gomock.Any(), "box-1").Return(true, &model.ToolboxDB{
		BoxID: "box-1", Status: string(interfaces.BizStatusPublished), ServerURL: "http://box.internal",
		MetadataType: string(interfaces.MetadataTypeFunc),
	}, nil)
	tools.EXPECT().SelectTool(gomock.Any(), "tool-1").Return(true, &model.ToolDB{
		ToolID: "tool-1", BoxID: "box-1", Name: "send_sms", Description: "Send an SMS",
		SourceID: "src-1", SourceType: model.SourceTypeFunction, Status: string(interfaces.ToolStatusTypeEnabled),
	}, nil)
	metadataService.EXPECT().GetMetadataBySource(gomock.Any(), "src-1", model.SourceTypeFunction).
		Return(true, &model.FunctionMetadataDB{
			Path:      "/internal/run/src-1",
			ServerURL: "http://function-runtime.internal",
			Method:    http.MethodPost,
			Code:      "def handler(event): return TOP_SECRET_SOURCE",
			APISpec: `{"parameters":[{"name":"phone","in":"query","description":"Recipient","required":true,` +
				`"schema":{"type":"string"}}],"request_body":{"description":"body","required":true,` +
				`"content":{"application/json":{"schema":{"type":"object","properties":{"text":{"type":"string"}}}}}},` +
				`"responses":[{"status_code":"200","description":"sent","content":{"application/json":` +
				`{"schema":{"type":"object"}}}}],"security":[{"internal_key":[]}]}`,
		}, nil)

	resp, err := svc.GetToolDefinitionAsProxy(toolDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
		&interfaces.GetToolDefinitionReq{BoxID: "box-1", ToolID: "tool-1"})
	if err != nil {
		t.Fatalf("GetToolDefinitionAsProxy() error = %v", err)
	}
	if resp.BoxID != "box-1" || resp.ToolID != "tool-1" || resp.Name != "send_sms" || resp.Description != "Send an SMS" {
		t.Fatalf("identity = %+v", resp)
	}
	if resp.APISpec == nil || len(resp.APISpec.Parameters) != 1 || resp.APISpec.Parameters[0].Name != "phone" ||
		resp.APISpec.RequestBody == nil || len(resp.APISpec.Responses) != 1 {
		t.Fatalf("api_spec = %+v", resp.APISpec)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"TOP_SECRET_SOURCE", "function-runtime.internal", "box.internal",
		"/internal/run", "internal_key", "function_content", "server_url"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("definition leaks %q: %s", leaked, encoded)
		}
	}
	if len(audit.events) != 1 || audit.events[0].Decision != "allow" ||
		audit.events[0].Access != interfaces.ProxyAccessDefinitionRead {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

// The proxy's grant is on the box its action type is bound to, and on what it
// could run now. A tool of another box, an offline box and a disabled tool are
// all outside it; none reaches the metadata table.
func TestGetToolDefinitionAsProxyStaysInsideARunnableBoundBox(t *testing.T) {
	tests := []struct {
		name       string
		box        *model.ToolboxDB
		tool       *model.ToolDB
		wantStatus int
		wantCode   string
	}{
		{
			name:       "tool of another box",
			box:        &model.ToolboxDB{BoxID: "box-1", Status: string(interfaces.BizStatusPublished)},
			tool:       &model.ToolDB{ToolID: "tool-1", BoxID: "box-2", Status: string(interfaces.ToolStatusTypeEnabled)},
			wantStatus: http.StatusNotFound,
			wantCode:   "ToolNotFound",
		},
		{
			name:       "offline box",
			box:        &model.ToolboxDB{BoxID: "box-1", Status: string(interfaces.BizStatusOffline)},
			wantStatus: http.StatusBadRequest,
			wantCode:   "ToolNotAvailable",
		},
		{
			name:       "disabled tool",
			box:        &model.ToolboxDB{BoxID: "box-1", Status: string(interfaces.BizStatusPublished)},
			tool:       &model.ToolDB{ToolID: "tool-1", BoxID: "box-1", Status: string(interfaces.ToolStatusTypeDisabled)},
			wantStatus: http.StatusBadRequest,
			wantCode:   "ToolNotAvailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			boxes := mocks.NewMockIToolboxDB(ctrl)
			tools := mocks.NewMockIToolDB(ctrl)
			boxes.EXPECT().SelectToolBox(gomock.Any(), "box-1").Return(true, test.box, nil)
			if test.tool != nil {
				tools.EXPECT().SelectTool(gomock.Any(), "tool-1").Return(true, test.tool, nil)
			}
			svc := &ToolServiceImpl{
				Logger:          logger.DefaultLogger(),
				ToolBoxDB:       boxes,
				ToolDB:          tools,
				MetadataService: mocks.NewMockIMetadataService(ctrl),
				ProxyAuthorizer: &definitionReadAuthorizer{},
				ProxyAudit:      &definitionReadAudit{},
			}

			_, err := svc.GetToolDefinitionAsProxy(toolDefinitionReadContext(interfaces.ProxyAccessDefinitionRead),
				&interfaces.GetToolDefinitionReq{BoxID: "box-1", ToolID: "tool-1"})

			if httpStatusOf(err) != test.wantStatus || !strings.Contains(err.Error(), test.wantCode) {
				t.Fatalf("GetToolDefinitionAsProxy() error = %v, want HTTP %d %s", err, test.wantStatus, test.wantCode)
			}
		})
	}
}
