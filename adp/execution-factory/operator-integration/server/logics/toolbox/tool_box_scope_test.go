package toolbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

// Every route that names a tool also names the toolbox it is addressed through.
// These tests pin that a tool is reached only through the toolbox that stores
// it: a tool of another toolbox is answered exactly like a missing one, and is
// never read, changed or run.
const (
	scopeUserID       = "user-1"
	scopeBoxID        = "box-a"
	scopeBoxServerURL = "http://box-a.example"
	scopeToolID       = "tool-a" // stored in box-a
	scopeToolName     = "tool a"
	scopeOtherToolID  = "tool-b" // stored in box-b
	scopeMissingID    = "tool-missing"
	scopeToolPath     = "/run"
)

type boxScopeFixture struct {
	svc          *ToolServiceImpl
	proxied      []*interfaces.HTTPRequest
	updatedTools []string
}

func newBoxScopeFixture(t *testing.T) *boxScopeFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	toolBoxDB := mocks.NewMockIToolboxDB(ctrl)
	toolDB := mocks.NewMockIToolDB(ctrl)
	metadataService := mocks.NewMockIMetadataService(ctrl)
	proxy := mocks.NewMockProxyHandler(ctrl)
	fixture := &boxScopeFixture{}

	accessor := &interfaces.AuthAccessor{ID: scopeUserID}
	auth.EXPECT().GetAccessor(gomock.Any(), scopeUserID).Return(accessor, nil).AnyTimes()
	// Every grant below is on box-a only.
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, scopeBoxID, interfaces.AuthResourceTypeToolBox).
		Return(nil).AnyTimes()
	auth.EXPECT().CheckModifyPermission(gomock.Any(), accessor, scopeBoxID, interfaces.AuthResourceTypeToolBox).
		Return(nil).AnyTimes()
	auth.EXPECT().OperationCheckAny(gomock.Any(), accessor, scopeBoxID, interfaces.AuthResourceTypeToolBox,
		interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess, interfaces.AuthOperationTypeExecute).
		Return(true, nil).AnyTimes()

	toolBoxDB.EXPECT().SelectToolBox(gomock.Any(), scopeBoxID).
		DoAndReturn(func(context.Context, string) (bool, *model.ToolboxDB, error) {
			return true, &model.ToolboxDB{
				BoxID:        scopeBoxID,
				Name:         "box a",
				ServerURL:    scopeBoxServerURL,
				MetadataType: string(interfaces.MetadataTypeAPI),
				Status:       string(interfaces.BizStatusPublished),
			}, nil
		}).AnyTimes()

	// tool-id lookups return the stored row, whichever toolbox it belongs to.
	toolDB.EXPECT().SelectTool(gomock.Any(), scopeToolID).
		DoAndReturn(func(context.Context, string) (bool, *model.ToolDB, error) {
			return true, &model.ToolDB{
				ToolID: scopeToolID, BoxID: scopeBoxID, Name: scopeToolName,
				SourceID: "source-a", SourceType: model.SourceTypeOpenAPI,
				Status: string(interfaces.ToolStatusTypeEnabled),
			}, nil
		}).AnyTimes()
	toolDB.EXPECT().SelectTool(gomock.Any(), scopeOtherToolID).
		DoAndReturn(func(context.Context, string) (bool, *model.ToolDB, error) {
			return true, &model.ToolDB{
				ToolID: scopeOtherToolID, BoxID: "box-b", Name: "tool b",
				SourceID: "source-b", SourceType: model.SourceTypeOpenAPI,
				Status: string(interfaces.ToolStatusTypeEnabled),
			}, nil
		}).AnyTimes()
	toolDB.EXPECT().SelectTool(gomock.Any(), scopeMissingID).Return(false, nil, nil).AnyTimes()
	toolDB.EXPECT().SelectBoxToolByName(gomock.Any(), scopeBoxID, gomock.Any()).Return(false, nil, nil).AnyTimes()
	toolDB.EXPECT().UpdateTool(gomock.Any(), gomock.Nil(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ any, tool *model.ToolDB) error {
			fixture.updatedTools = append(fixture.updatedTools, tool.ToolID)
			return nil
		}).AnyTimes()

	// Only box-a's own tool has its metadata resolved; reading source-b would be
	// an unexpected call and fails the test.
	metadataService.EXPECT().GetMetadataBySource(gomock.Any(), "source-a", model.SourceTypeOpenAPI).
		DoAndReturn(func(context.Context, string, model.SourceType) (bool, interfaces.IMetadataDB, error) {
			return true, &model.APIMetadataDB{
				Version: "source-a", ServerURL: "http://metadata.example",
				Path: scopeToolPath, Method: http.MethodPost,
			}, nil
		}).AnyTimes()

	proxy.EXPECT().HandlerRequest(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
			fixture.proxied = append(fixture.proxied, req)
			return &interfaces.HTTPResponse{StatusCode: http.StatusOK}, nil
		}).AnyTimes()

	fixture.svc = &ToolServiceImpl{
		Logger:          logger.DefaultLogger(),
		AuthService:     auth,
		ToolBoxDB:       toolBoxDB,
		ToolDB:          toolDB,
		MetadataService: metadataService,
		Proxy:           proxy,
		AuditLog:        mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl),
	}
	return fixture
}

// boxScopedToolPath is one entry point that addresses a tool through a toolbox.
// call reports whether a result came back, and returns the error.
type boxScopedToolPath struct {
	name string
	call func(ctx context.Context, svc *ToolServiceImpl, toolID string) (bool, error)
	// checkSameBox verifies the legitimate request did its work.
	checkSameBox func(t *testing.T, fixture *boxScopeFixture)
}

func boxScopedToolPaths() []boxScopedToolPath {
	executeReq := func(toolID string) *interfaces.ExecuteToolReq {
		return &interfaces.ExecuteToolReq{UserID: scopeUserID, BoxID: scopeBoxID, ToolID: toolID}
	}
	proxiedOnceToBox := func(t *testing.T, fixture *boxScopeFixture) {
		t.Helper()
		if len(fixture.proxied) != 1 {
			t.Fatalf("proxied %d requests, want 1", len(fixture.proxied))
		}
		if got, want := fixture.proxied[0].URL, scopeBoxServerURL+scopeToolPath; got != want {
			t.Fatalf("proxied to %q, want %q", got, want)
		}
	}
	return []boxScopedToolPath{
		{
			name: "GetBoxTool",
			call: func(ctx context.Context, svc *ToolServiceImpl, toolID string) (bool, error) {
				resp, err := svc.GetBoxTool(common.SetPublicAPIToCtx(ctx, true),
					&interfaces.GetToolReq{UserID: scopeUserID, BoxID: scopeBoxID, ToolID: toolID})
				return resp != nil, err
			},
		},
		{
			name: "UpdateTool",
			call: func(ctx context.Context, svc *ToolServiceImpl, toolID string) (bool, error) {
				resp, err := svc.UpdateTool(ctx, &interfaces.UpdateToolReq{
					UserID: scopeUserID, BoxID: scopeBoxID, ToolID: toolID,
					ToolName: scopeToolName, ToolDesc: "updated", MetadataType: interfaces.MetadataTypeAPI,
				})
				return resp != nil, err
			},
			checkSameBox: func(t *testing.T, fixture *boxScopeFixture) {
				t.Helper()
				if len(fixture.updatedTools) != 1 || fixture.updatedTools[0] != scopeToolID {
					t.Fatalf("updated tools %v, want [%s]", fixture.updatedTools, scopeToolID)
				}
			},
		},
		{
			name: "DebugTool",
			call: func(ctx context.Context, svc *ToolServiceImpl, toolID string) (bool, error) {
				resp, err := svc.DebugTool(ctx, executeReq(toolID))
				return resp != nil, err
			},
			checkSameBox: proxiedOnceToBox,
		},
		{
			name: "ExecuteTool",
			call: func(ctx context.Context, svc *ToolServiceImpl, toolID string) (bool, error) {
				resp, err := svc.ExecuteTool(ctx, executeReq(toolID))
				return resp != nil, err
			},
			checkSameBox: proxiedOnceToBox,
		},
		{
			// The execution entry used by MCP servers built from toolbox tools.
			name: "ExecuteToolCore",
			call: func(ctx context.Context, svc *ToolServiceImpl, toolID string) (bool, error) {
				resp, err := svc.ExecuteToolCore(ctx, executeReq(toolID))
				return resp != nil, err
			},
			checkSameBox: proxiedOnceToBox,
		},
	}
}

func TestBoxScopedToolPathsServeTheirOwnTool(t *testing.T) {
	for _, path := range boxScopedToolPaths() {
		t.Run(path.name, func(t *testing.T) {
			fixture := newBoxScopeFixture(t)
			found, err := path.call(context.Background(), fixture.svc, scopeToolID)
			if err != nil {
				t.Fatalf("%s(%s, %s) failed: %v", path.name, scopeBoxID, scopeToolID, err)
			}
			if !found {
				t.Fatalf("%s(%s, %s) returned no result", path.name, scopeBoxID, scopeToolID)
			}
			if path.checkSameBox != nil {
				path.checkSameBox(t, fixture)
			}
		})
	}
}

func TestBoxScopedToolPathsTreatToolOfAnotherBoxAsMissing(t *testing.T) {
	for _, path := range boxScopedToolPaths() {
		t.Run(path.name, func(t *testing.T) {
			fixture := newBoxScopeFixture(t)

			_, missingErr := path.call(context.Background(), fixture.svc, scopeMissingID)
			missing := requireHTTPError(t, missingErr)
			if !strings.HasSuffix(missing.Code, string(oerrors.ErrExtToolNotFound)) {
				t.Fatalf("missing tool answered %q, want a %s error", missing.Code, oerrors.ErrExtToolNotFound)
			}

			found, otherErr := path.call(context.Background(), fixture.svc, scopeOtherToolID)
			if found {
				t.Fatalf("%s(%s, %s) returned a result for a tool of another toolbox", path.name, scopeBoxID, scopeOtherToolID)
			}
			other := requireHTTPError(t, otherErr)
			if other.HTTPCode != missing.HTTPCode || other.Code != missing.Code || other.Description != missing.Description {
				t.Fatalf("tool of another toolbox answered %d %q %q, want the missing-tool answer %d %q %q",
					other.HTTPCode, other.Code, other.Description, missing.HTTPCode, missing.Code, missing.Description)
			}
			if got, want := other.ErrorDetails, fmt.Sprintf("tool %s not found", scopeOtherToolID); got != want {
				t.Fatalf("details = %v, want %q", got, want)
			}
			if len(fixture.proxied) != 0 {
				t.Fatalf("a tool of another toolbox was run: %d proxied requests", len(fixture.proxied))
			}
			if len(fixture.updatedTools) != 0 {
				t.Fatalf("a tool of another toolbox was changed: %v", fixture.updatedTools)
			}
		})
	}
}

func requireHTTPError(t *testing.T, err error) *oerrors.HTTPError {
	t.Helper()
	var httpErr *oerrors.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want an HTTP error", err)
	}
	return httpErr
}
