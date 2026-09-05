// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/url"
	"strconv"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func toolsTestClient(t *testing.T) (*operatorIntegrationClient, *mocks.MockHTTPClient) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
	return &operatorIntegrationClient{
		logger:     mockLogger,
		baseURL:    "http://operator/api/agent-operator-integration",
		httpClient: mockHTTPClient,
	}, mockHTTPClient
}

// managedCallContext is what the MCP lifecycle guard leaves on the context
// before a business tool handler runs: the caller's own token plus the
// Conversation and Interaction the turn belongs to.
func managedCallContext() context.Context {
	ctx := common.SetRawTokenToCtx(context.Background(), "caller-token")
	return common.SetTraceContextToCtx(ctx, common.TraceContext{
		RequestID:      "req_1",
		ConversationID: "conv_1",
		InteractionID:  "int_1",
		OperationID:    "op_1",
		Attempt:        1,
	})
}

// The whole point of executing a published Function through Context Loader is
// that it runs as the caller, inside the Interaction that asked for it. Without
// these three headers the Function reaches the sandbox with no credential and
// no session, which is what #1193 reports as a broken business call.
func TestExecutePublishedToolCarriesCallerAndInteraction(t *testing.T) {
	convey.Convey("execute carries the caller token and the managed Interaction", t, func() {
		client, httpClient := toolsTestClient(t)
		var gotURL string
		var gotHeader map[string]string
		var gotBody any

		httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, url string, header map[string]string, body any) (int, any, error) {
				gotURL, gotHeader, gotBody = url, header, body
				return 200, map[string]any{"status_code": 200}, nil
			})

		resp, err := client.ExecutePublishedTool(managedCallContext(), &interfaces.ExecutePublishedToolRequest{
			ToolboxID:  "box_1",
			ToolID:     "tool_1",
			Parameters: map[string]any{"material_code": "M-1"},
		})

		convey.So(err, convey.ShouldBeNil)
		convey.So(resp["status_code"], convey.ShouldEqual, 200)
		convey.So(gotURL, convey.ShouldEqual,
			"http://operator/api/agent-operator-integration/v1/tool-box/box_1/proxy/tool_1")
		convey.So(gotHeader["Authorization"], convey.ShouldEqual, "Bearer caller-token")
		convey.So(gotHeader["bkn-conversation-id"], convey.ShouldEqual, "conv_1")
		convey.So(gotHeader["bkn-interaction-id"], convey.ShouldEqual, "int_1")
		convey.So(gotHeader["bkn-parent-operation-id"], convey.ShouldEqual, "op_1")
		convey.So(gotHeader["bkn-operation-id"], convey.ShouldNotEqual, "op_1")

		// The Toolbox proxy expects HTTPRequestParams: business parameters go in
		// its body field, or the proxied request arrives with an empty body.
		envelope, ok := gotBody.(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(envelope["body"], convey.ShouldResemble, map[string]any{"material_code": "M-1"})
	})
}

// The catalogue is read as the caller too, so an account only ever discovers
// what Execution Factory would let it call.
func TestPublishedCatalogueReadsAsTheCaller(t *testing.T) {
	convey.Convey("listing toolboxes and tools carries the caller token", t, func() {
		client, httpClient := toolsTestClient(t)
		var headers []map[string]string

		httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ any, header map[string]string) (int, any, error) {
				headers = append(headers, header)
				return 200, map[string]any{"data": []any{}, "tools": []any{}}, nil
			}).Times(2)

		ctx := managedCallContext()
		_, err := client.ListPublishedToolboxes(ctx, &interfaces.ListPublishedToolboxesRequest{})
		convey.So(err, convey.ShouldBeNil)
		_, err = client.ListPublishedTools(ctx, &interfaces.ListPublishedToolsRequest{ToolboxID: "box_1"})
		convey.So(err, convey.ShouldBeNil)

		for _, header := range headers {
			convey.So(header["Authorization"], convey.ShouldEqual, "Bearer caller-token")
		}
	})
}

// Falling back to this service's own identity would disclose another caller's
// inventory and skip Execution Factory's execute check, so a request with no
// caller token must fail rather than degrade.
func TestPublishedToolSurfaceRefusesWithoutACallerToken(t *testing.T) {
	convey.Convey("no caller token means no call at all", t, func() {
		client, _ := toolsTestClient(t)
		ctx := context.Background()

		_, err := client.ListPublishedToolboxes(ctx, &interfaces.ListPublishedToolboxesRequest{})
		convey.So(err, convey.ShouldNotBeNil)
		_, err = client.ListPublishedTools(ctx, &interfaces.ListPublishedToolsRequest{ToolboxID: "box_1"})
		convey.So(err, convey.ShouldNotBeNil)
		_, err = client.ExecutePublishedTool(ctx, &interfaces.ExecutePublishedToolRequest{
			ToolboxID: "box_1", ToolID: "tool_1",
		})
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// execute_tool checks its tool against this catalogue before calling it, so a
// catalogue that stops at one page would refuse the 101st enabled tool as if it
// were disabled. The walk has to reach the end.
func TestPublishedCatalogueWalksEveryPage(t *testing.T) {
	convey.Convey("paging continues until a short page", t, func() {
		client, httpClient := toolsTestClient(t)

		fullPage := make([]any, 0, 100)
		for i := 0; i < 100; i++ {
			fullPage = append(fullPage, map[string]any{
				"tool_id": "tool_" + strconv.Itoa(i), "name": "t", "status": "enabled",
			})
		}
		lastPage := []any{map[string]any{"tool_id": "tool_100", "name": "the-101st", "status": "enabled"}}

		var pages []string
		gomock.InOrder(
			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, query any, _ map[string]string) (int, any, error) {
					pages = append(pages, query.(url.Values).Get("page"))
					return 200, map[string]any{"tools": fullPage}, nil
				}),
			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, query any, _ map[string]string) (int, any, error) {
					pages = append(pages, query.(url.Values).Get("page"))
					return 200, map[string]any{"tools": lastPage}, nil
				}),
		)

		resp, err := client.ListPublishedTools(managedCallContext(),
			&interfaces.ListPublishedToolsRequest{ToolboxID: "box_1"})

		convey.So(err, convey.ShouldBeNil)
		convey.So(pages, convey.ShouldResemble, []string{"1", "2"})
		convey.So(len(resp.Tools), convey.ShouldEqual, 101)
		convey.So(resp.Tools[100].Name, convey.ShouldEqual, "the-101st")
	})
}

// A Function's business-input contract is what a model needs; the rest of the
// public metadata is service topology and costs context for nothing.
func TestListPublishedToolsTrimsMetadataToBusinessInput(t *testing.T) {
	convey.Convey("only the business input contract survives", t, func() {
		client, httpClient := toolsTestClient(t)

		httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]any{
				"box_id": "box_1",
				"tools": []any{
					map[string]any{
						"tool_id": "tool_1", "name": "fx_convert", "status": "enabled",
						"metadata": map[string]any{
							"server_url": "http://internal-only",
							"api_spec": map[string]any{
								"parameters":   []any{},
								"request_body": map[string]any{"required": true},
								"servers":      []any{map[string]any{"url": "http://internal-only"}},
								"components":   map[string]any{"schemas": map[string]any{"In": map[string]any{}}},
							},
						},
					},
					map[string]any{"tool_id": "tool_2", "name": "disabled_one", "status": "disabled"},
				},
			}, nil)

		resp, err := client.ListPublishedTools(managedCallContext(),
			&interfaces.ListPublishedToolsRequest{ToolboxID: "box_1"})

		convey.So(err, convey.ShouldBeNil)
		convey.So(len(resp.Tools), convey.ShouldEqual, 1)
		schema := resp.Tools[0].InputSchema
		convey.So(schema["parameters"], convey.ShouldNotBeNil)
		convey.So(schema["request_body"], convey.ShouldNotBeNil)
		convey.So(schema["components"], convey.ShouldNotBeNil)
		convey.So(schema["servers"], convey.ShouldBeNil)
	})
}
