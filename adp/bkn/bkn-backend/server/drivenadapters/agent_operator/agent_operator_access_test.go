// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package agent_operator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
)

func TestGetToolByIDClassifiesSafeDependencyErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       []byte
		requestErr error
		kind       interfaces.DependencyErrorKind
	}{
		{name: "forbidden", status: http.StatusForbidden, body: []byte(`{"secret":"policy"}`), kind: interfaces.DependencyForbidden},
		{name: "not found", status: http.StatusNotFound, kind: interfaces.DependencyNotFound},
		{name: "timeout", requestErr: context.DeadlineExceeded, kind: interfaces.DependencyTimeout},
		{name: "unavailable", requestErr: errors.New("dial tcp 10.0.0.1: refused"), kind: interfaces.DependencyUnavailable},
		{name: "downstream 5xx", status: http.StatusInternalServerError, body: []byte(`{"secret":"stack"}`), kind: interfaces.DependencyDownstreamError},
		{name: "invalid response", status: http.StatusOK, body: []byte(`not-json-secret`), kind: interfaces.DependencyInvalidResponse},
		{name: "missing tools field", status: http.StatusOK, body: []byte(`{}`), kind: interfaces.DependencyInvalidResponse},
		{name: "null tools field", status: http.StatusOK, body: []byte(`{"tools":null}`), kind: interfaces.DependencyInvalidResponse},
		{name: "invalid tool entry", status: http.StatusOK, body: []byte(`{"tools":[{"name":""}]}`), kind: interfaces.DependencyInvalidResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tc.status, tc.body, tc.requestErr)
			access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

			err := access.GetToolByID(t.Context(), "box-1", "tool-1")

			var dependencyErr *interfaces.DependencyError
			require.ErrorAs(t, err, &dependencyErr)
			assert.Equal(t, tc.kind, dependencyErr.Kind)
			assert.NotContains(t, err.Error(), "secret")
			assert.NotContains(t, err.Error(), "10.0.0.1")
		})
	}
}

func TestGetMcpToolByNameTreatsExplicitEmptyToolListAsNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	gomock.InOrder(
		httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://execution/mcp/mcp-1", nil, gomock.Any()).
			Return(http.StatusOK, []byte(`{"base_info":{"mcp_id":"mcp-1","name":"orders","status":"published"}}`), nil),
		httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://execution/mcp/proxy/mcp-1/tools", nil, gomock.Any()).
			Return(http.StatusOK, []byte(`{"tools":[]}`), nil),
	)
	access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

	err := access.GetMcpToolByName(t.Context(), "mcp-1", "create_order")

	var dependencyErr *interfaces.DependencyError
	require.ErrorAs(t, err, &dependencyErr)
	assert.Equal(t, interfaces.DependencyNotFound, dependencyErr.Kind)
}

func TestGetToolByIDAcceptsMatchingToolResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := rmock.NewMockHTTPClient(ctrl)
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(`{"tool_id":"tool-1"}`), nil)
	access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

	require.NoError(t, access.GetToolByID(t.Context(), "box-1", "tool-1"))
}

func TestGetMcpToolByNameClassifiesServerLookupErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       []byte
		requestErr error
		kind       interfaces.DependencyErrorKind
	}{
		{name: "not found", status: http.StatusNotFound, kind: interfaces.DependencyNotFound},
		{name: "forbidden", status: http.StatusForbidden, body: []byte(`{"secret":"policy"}`), kind: interfaces.DependencyForbidden},
		{name: "timeout", requestErr: context.DeadlineExceeded, kind: interfaces.DependencyTimeout},
		{name: "downstream 5xx", status: http.StatusInternalServerError, body: []byte(`{"secret":"stack"}`), kind: interfaces.DependencyDownstreamError},
		{name: "invalid response", status: http.StatusOK, body: []byte(`not-json-secret`), kind: interfaces.DependencyInvalidResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://execution/mcp/mcp-1", nil, gomock.Any()).
				Return(tc.status, tc.body, tc.requestErr)
			access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

			err := access.GetMcpToolByName(t.Context(), "mcp-1", "create_order")

			var dependencyErr *interfaces.DependencyError
			require.ErrorAs(t, err, &dependencyErr)
			assert.Equal(t, tc.kind, dependencyErr.Kind)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestGetMcpToolByNameClassifiesToolListingErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       []byte
		requestErr error
		kind       interfaces.DependencyErrorKind
	}{
		{name: "not found", status: http.StatusNotFound, kind: interfaces.DependencyNotFound},
		{name: "unavailable", requestErr: errors.New("dial tcp 10.0.0.1: refused"), kind: interfaces.DependencyUnavailable},
		{name: "downstream 5xx", status: http.StatusInternalServerError, body: []byte(`{"secret":"stack"}`), kind: interfaces.DependencyDownstreamError},
		{name: "invalid response", status: http.StatusOK, body: []byte(`not-json-secret`), kind: interfaces.DependencyInvalidResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			gomock.InOrder(
				httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://execution/mcp/mcp-1", nil, gomock.Any()).
					Return(http.StatusOK, []byte(`{"base_info":{"mcp_id":"mcp-1","name":"orders","status":"published"}}`), nil),
				httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://execution/mcp/proxy/mcp-1/tools", nil, gomock.Any()).
					Return(tc.status, tc.body, tc.requestErr),
			)
			access := &agentOperatorAccess{agentOperatorURL: "http://execution", httpClient: httpClient}

			err := access.GetMcpToolByName(t.Context(), "mcp-1", "create_order")

			var dependencyErr *interfaces.DependencyError
			require.ErrorAs(t, err, &dependencyErr)
			assert.Equal(t, tc.kind, dependencyErr.Kind)
			assert.NotContains(t, err.Error(), "secret")
			assert.NotContains(t, err.Error(), "10.0.0.1")
		})
	}
}
