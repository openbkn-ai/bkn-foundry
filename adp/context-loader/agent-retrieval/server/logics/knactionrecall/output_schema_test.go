// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// newOutputSchemaService builds a service whose downstreams are wired for a single
// get_action_info call against the given action source and tool detail.
func newOutputSchemaService(t *testing.T) (*knActionRecallServiceImpl, *mocks.MockDrivenOntologyQuery, *mocks.MockDrivenOperatorIntegration) {
	t.Helper()
	ctrl := gomock.NewController(t)

	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

	mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
	mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)

	service := &knActionRecallServiceImpl{
		logger: mockLogger,
		config: &config.Config{
			OntologyQuery: config.PrivateBaseConfig{
				PrivateProtocol: "http",
				PrivateHost:     "ontology-query",
				PrivatePort:     13018,
			},
		},
		ontologyQuery:       mockOntologyQuery,
		operatorIntegration: mockOperatorIntegration,
	}
	return service, mockOntologyQuery, mockOperatorIntegration
}

func toolActionsResponse() *interfaces.QueryActionsResponse {
	return &interfaces.QueryActionsResponse{
		ActionSource: &interfaces.ActionSource{
			Type:   interfaces.ActionSourceTypeTool,
			BoxID:  "box-001",
			ToolID: "tool-001",
		},
		Actions: []interfaces.ActionParams{{Parameters: map[string]any{"stock_id": 903}}},
	}
}

func mcpActionsResponse() *interfaces.QueryActionsResponse {
	return &interfaces.QueryActionsResponse{
		ActionSource: &interfaces.ActionSource{
			Type:     interfaces.ActionSourceTypeMCP,
			McpID:    "mcp-001",
			ToolName: "issue_voucher",
		},
		Actions: []interfaces.ActionParams{{Parameters: map[string]any{"stock_id": 903}}},
	}
}

func recallRequest() *interfaces.KnActionRecallRequest {
	return &interfaces.KnActionRecallRequest{
		KnID:               "kn-001",
		AtID:               "at-001",
		InstanceIdentities: []map[string]any{{"id": 903}},
	}
}

// jsonResponses builds an api_spec responses array carrying one JSON response.
func jsonResponses(statusCode string, schema map[string]any) []any {
	return []any{
		map[string]any{
			"status_code": statusCode,
			"content": map[string]any{
				"application/json": map[string]any{"schema": schema},
			},
		},
	}
}

// TestOutputSchema_OpenAPITool_ResolvesRefAndKeepsDescriptions verifies that an OpenAPI tool's
// 200 response becomes output_schema with $ref inlined and description/enum preserved.
func TestOutputSchema_OpenAPITool_ResolvesRefAndKeepsDescriptions(t *testing.T) {
	convey.Convey("TestOutputSchema_OpenAPITool_ResolvesRefAndKeepsDescriptions", t, func() {
		service, ontologyQuery, operatorIntegration := newOutputSchemaService(t)

		ontologyQuery.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(toolActionsResponse(), nil)
		operatorIntegration.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(&interfaces.GetToolDetailResponse{
			Name:         "issue_voucher",
			MetadataType: "openapi",
			Metadata: interfaces.ToolMetadata{
				APISpec: map[string]any{
					"parameters": []any{
						map[string]any{"name": "unit_price", "in": "query", "schema": map[string]any{"type": "number"}},
					},
					"responses": jsonResponses("200", map[string]any{"$ref": "#/components/schemas/VoucherResult"}),
					"components": map[string]any{
						"schemas": map[string]any{
							"VoucherResult": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"accepted":  map[string]any{"type": "boolean", "description": "whether the request was accepted"},
									"recordId":  map[string]any{"type": "integer", "description": "generated voucher id"},
									"stateflow": map[string]any{"type": "string", "enum": []any{"reserved", "issued"}},
								},
							},
						},
					},
				},
			},
		}, nil)

		resp, err := service.GetActionInfo(context.Background(), recallRequest())
		convey.So(err, convey.ShouldBeNil)

		output := resp.DynamicTools[0].OutputSchema
		convey.So(output, convey.ShouldNotBeNil)
		convey.So(output["type"], convey.ShouldEqual, "object")
		convey.So(output["description"], convey.ShouldNotBeBlank)

		props, ok := output["properties"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(props), convey.ShouldEqual, 3)

		accepted, ok := props["accepted"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(accepted["description"], convey.ShouldEqual, "whether the request was accepted")

		stateflow, ok := props["stateflow"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(stateflow["enum"], convey.ShouldNotBeNil)
	})
}

// TestOutputSchema_FunctionTool_StripsResultEnvelope verifies that a function tool's business
// payload is lifted out of the result envelope.
func TestOutputSchema_FunctionTool_StripsResultEnvelope(t *testing.T) {
	convey.Convey("TestOutputSchema_FunctionTool_StripsResultEnvelope", t, func() {
		service, ontologyQuery, operatorIntegration := newOutputSchemaService(t)

		ontologyQuery.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(toolActionsResponse(), nil)
		operatorIntegration.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(&interfaces.GetToolDetailResponse{
			Name:         "compute_quota",
			MetadataType: "function",
			Metadata: interfaces.ToolMetadata{
				APISpec: map[string]any{
					"responses": jsonResponses("200", map[string]any{
						"type": "object",
						"properties": map[string]any{
							"code": map[string]any{"type": "integer"},
							"result": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"quota": map[string]any{"type": "number", "description": "computed quota"},
								},
							},
						},
					}),
				},
			},
		}, nil)

		resp, err := service.GetActionInfo(context.Background(), recallRequest())
		convey.So(err, convey.ShouldBeNil)

		props, ok := resp.DynamicTools[0].OutputSchema["properties"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(props), convey.ShouldEqual, 1)
		convey.So(props["quota"], convey.ShouldNotBeNil)
		convey.So(props["code"], convey.ShouldBeNil)
	})
}

// TestOutputSchema_FallsBackToOther2xx verifies that a spec without a 200 response still
// yields an output schema from another 2xx response.
func TestOutputSchema_FallsBackToOther2xx(t *testing.T) {
	convey.Convey("TestOutputSchema_FallsBackToOther2xx", t, func() {
		service, ontologyQuery, operatorIntegration := newOutputSchemaService(t)

		ontologyQuery.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(toolActionsResponse(), nil)
		operatorIntegration.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(&interfaces.GetToolDetailResponse{
			Name:         "issue_voucher",
			MetadataType: "openapi",
			Metadata: interfaces.ToolMetadata{
				APISpec: map[string]any{
					"responses": jsonResponses("201", map[string]any{
						"type":       "object",
						"properties": map[string]any{"recordId": map[string]any{"type": "integer"}},
					}),
				},
			},
		}, nil)

		resp, err := service.GetActionInfo(context.Background(), recallRequest())
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.DynamicTools[0].OutputSchema, convey.ShouldNotBeNil)
	})
}

// TestOutputSchema_OmittedWhenUnavailable covers every case where no usable shape exists:
// the field must stay absent rather than become an empty object.
func TestOutputSchema_OmittedWhenUnavailable(t *testing.T) {
	cases := []struct {
		name    string
		apiSpec map[string]any
	}{
		{
			name:    "no responses at all",
			apiSpec: map[string]any{"parameters": []any{}},
		},
		{
			name: "only error responses",
			apiSpec: map[string]any{
				"responses": jsonResponses("400", map[string]any{
					"type":       "object",
					"properties": map[string]any{"message": map[string]any{"type": "string"}},
				}),
			},
		},
		{
			name: "non JSON media type",
			apiSpec: map[string]any{
				"responses": []any{
					map[string]any{
						"status_code": "200",
						"content": map[string]any{
							"text/csv": map[string]any{"schema": map[string]any{"type": "string"}},
						},
					},
				},
			},
		},
		{
			name:    "object without properties",
			apiSpec: map[string]any{"responses": jsonResponses("200", map[string]any{"type": "object"})},
		},
	}

	for _, tc := range cases {
		convey.Convey("TestOutputSchema_OmittedWhenUnavailable/"+tc.name, t, func() {
			service, ontologyQuery, operatorIntegration := newOutputSchemaService(t)

			ontologyQuery.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(toolActionsResponse(), nil)
			operatorIntegration.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(&interfaces.GetToolDetailResponse{
				Name:         "issue_voucher",
				MetadataType: "openapi",
				Metadata:     interfaces.ToolMetadata{APISpec: tc.apiSpec},
			}, nil)

			resp, err := service.GetActionInfo(context.Background(), recallRequest())
			convey.So(err, convey.ShouldBeNil)
			convey.So(resp.DynamicTools[0].OutputSchema, convey.ShouldBeNil)
		})
	}
}

// TestOutputSchema_MCPTool_ForwardsOutputSchema verifies that an MCP tool's outputSchema is
// forwarded with #/$defs/* references inlined.
func TestOutputSchema_MCPTool_ForwardsOutputSchema(t *testing.T) {
	convey.Convey("TestOutputSchema_MCPTool_ForwardsOutputSchema", t, func() {
		service, ontologyQuery, operatorIntegration := newOutputSchemaService(t)

		ontologyQuery.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(mcpActionsResponse(), nil)
		operatorIntegration.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(&interfaces.GetMCPToolDetailResponse{
			Name:        "issue_voucher",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"unit_price": map[string]any{"type": "number"}}},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"receipt": map[string]any{"$ref": "#/$defs/Receipt"},
				},
				"$defs": map[string]any{
					"Receipt": map[string]any{
						"type":       "object",
						"properties": map[string]any{"recordId": map[string]any{"type": "integer"}},
					},
				},
			},
		}, nil)

		resp, err := service.GetActionInfo(context.Background(), recallRequest())
		convey.So(err, convey.ShouldBeNil)

		output := resp.DynamicTools[0].OutputSchema
		convey.So(output, convey.ShouldNotBeNil)
		convey.So(output["$defs"], convey.ShouldBeNil)

		props, ok := output["properties"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		receipt, ok := props["receipt"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		receiptProps, ok := receipt["properties"].(map[string]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(receiptProps["recordId"], convey.ShouldNotBeNil)
	})
}

// TestOutputSchema_MCPTool_AbsentWhenNotDeclared verifies that an MCP tool without an
// outputSchema leaves the field absent.
func TestOutputSchema_MCPTool_AbsentWhenNotDeclared(t *testing.T) {
	convey.Convey("TestOutputSchema_MCPTool_AbsentWhenNotDeclared", t, func() {
		service, ontologyQuery, operatorIntegration := newOutputSchemaService(t)

		ontologyQuery.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(mcpActionsResponse(), nil)
		operatorIntegration.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(&interfaces.GetMCPToolDetailResponse{
			Name:        "issue_voucher",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}, nil)

		resp, err := service.GetActionInfo(context.Background(), recallRequest())
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.DynamicTools[0].OutputSchema, convey.ShouldBeNil)
	})
}
