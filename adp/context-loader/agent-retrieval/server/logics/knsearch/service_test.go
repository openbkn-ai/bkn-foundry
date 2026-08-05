package knsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestLocalSearch_Service(t *testing.T) {
	// 准备基础测试数据
	mockDetail := createMockNetworkDetail(3, 3, 1)

	tests := []struct {
		name              string
		req               *interfaces.KnSearchLocalRequest
		mockSetup         func(*mockBknBackend, *mockOntologyQuery, *mockRerankClient)
		expectQueryCalled bool
		checkResult       func(*testing.T, *interfaces.KnSearchLocalResponse, error)
	}{
		{
			name: "Success - Instances Retrieved By Default",
			req: &interfaces.KnSearchLocalRequest{
				KnID:  "129",
				Query: "test",
			},
			mockSetup: func(m *mockBknBackend, q *mockOntologyQuery, r *mockRerankClient) {
				m.networkDetail = mockDetail
				// Mock instance retrieval success
				q.instancesResp = &interfaces.QueryObjectInstancesResp{
					Data: []any{
						map[string]any{
							"unique_identities": map[string]any{"id": "inst1"},
							"instance_name":     "test instance",
							"_score":            0.9,
						},
					},
				}
			},
			expectQueryCalled: true,
			checkResult: func(t *testing.T, res *interfaces.KnSearchLocalResponse, err error) {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				if len(res.ObjectTypes) == 0 {
					t.Error("Expected object types")
				}
				if len(res.Nodes) == 0 {
					t.Error("Expected instance nodes when only_schema is false")
				}
			},
		},
		{
			name: "Success - Only Schema",
			req: &interfaces.KnSearchLocalRequest{
				KnID:       "129",
				Query:      "test",
				OnlySchema: true,
			},
			mockSetup: func(m *mockBknBackend, q *mockOntologyQuery, r *mockRerankClient) {
				m.networkDetail = mockDetail
				// QueryObjectInstances should NOT be called
			},
			expectQueryCalled: false,
			checkResult: func(t *testing.T, res *interfaces.KnSearchLocalResponse, err error) {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				if len(res.ObjectTypes) == 0 {
					t.Error("Expected object types")
				}
				if len(res.Nodes) > 0 {
					t.Error("Expected 0 nodes in OnlySchema mode")
				}
				if res.Message != "" {
					t.Error("Expected empty message in OnlySchema mode")
				}
			},
		},
		{
			name: "Failure - Concept Retrieval Failed",
			req: &interfaces.KnSearchLocalRequest{
				KnID: "129",
			},
			mockSetup: func(m *mockBknBackend, q *mockOntologyQuery, r *mockRerankClient) {
				m.networkError = errors.New("network error")
			},
			expectQueryCalled: false,
			checkResult: func(t *testing.T, res *interfaces.KnSearchLocalResponse, err error) {
				if err == nil {
					t.Error("Expected error")
				}
				if res != nil {
					t.Error("Expected nil response")
				}
			},
		},
		{
			name: "Success - Instance Retrieval Failure Degrades To Schema",
			req: &interfaces.KnSearchLocalRequest{
				KnID:  "129",
				Query: "test",
			},
			mockSetup: func(m *mockBknBackend, q *mockOntologyQuery, r *mockRerankClient) {
				m.networkDetail = mockDetail
				q.instancesError = errors.New("query error")
			},
			expectQueryCalled: true,
			checkResult: func(t *testing.T, res *interfaces.KnSearchLocalResponse, err error) {
				if err != nil {
					t.Fatalf("Instance retrieval failure must not fail the whole search: %v", err)
				}
				if len(res.ObjectTypes) == 0 {
					t.Error("Expected object types to survive the degrade")
				}
				if len(res.Nodes) > 0 {
					t.Error("Expected 0 nodes when instance retrieval fails")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockManager := &mockBknBackend{}
			mockQuery := &mockOntologyQuery{}
			mockRerank := &mockRerankClient{}

			if tt.mockSetup != nil {
				tt.mockSetup(mockManager, mockQuery, mockRerank)
			}

			svc := &localSearchImpl{
				logger:        &mockLogger{},
				bknBackend:    mockManager,
				ontologyQuery: mockQuery,
				rerankClient:  mockRerank,
			}

			res, err := svc.Search(context.Background(), tt.req)
			tt.checkResult(t, res, err)

			if tt.expectQueryCalled && mockQuery.callCount == 0 {
				t.Fatal("Expected QueryObjectInstances to be called")
			}
			if !tt.expectQueryCalled && mockQuery.callCount != 0 {
				t.Fatalf("Expected QueryObjectInstances to not be called, got %d", mockQuery.callCount)
			}
		})
	}
}
