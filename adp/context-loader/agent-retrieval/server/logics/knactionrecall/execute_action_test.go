// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/mocks"
)

// TestExecuteAction_Success 透传 ontology-query 的执行响应
func TestExecuteAction_Success(t *testing.T) {
	convey.Convey("TestExecuteAction_Success", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:              mockLogger,
			config:              &config.Config{},
			ontologyQuery:       mockOntologyQuery,
			operatorIntegration: mockOperatorIntegration,
		}

		ctx := context.Background()
		req := &interfaces.KnActionExecuteRequest{
			KnID:               "kn-001",
			AtID:               "at-001",
			InstanceIdentities: []map[string]any{{"key_id": "14"}},
			DynamicParams:      map[string]any{"message": "hi", "name": "zhangsan"},
		}

		// 断言透传的 execReq 携带了动态参数与实例标识
		mockOntologyQuery.EXPECT().ExecuteActions(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, r *interfaces.ExecuteActionsRequest) (*interfaces.ExecuteActionsResponse, error) {
				convey.So(r.KnID, convey.ShouldEqual, "kn-001")
				convey.So(r.AtID, convey.ShouldEqual, "at-001")
				convey.So(r.DynamicParams["message"], convey.ShouldEqual, "hi")
				convey.So(r.DynamicParams["name"], convey.ShouldEqual, "zhangsan")
				convey.So(len(r.InstanceIdentities), convey.ShouldEqual, 1)
				return &interfaces.ExecuteActionsResponse{
					ExecutionID: "exec-xyz",
					Status:      "pending",
					Message:     "Action execution started",
					CreatedAt:   123,
				}, nil
			})

		resp, err := service.ExecuteAction(ctx, req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.ExecutionID, convey.ShouldEqual, "exec-xyz")
		convey.So(resp.Status, convey.ShouldEqual, "pending")
		convey.So(resp.CreatedAt, convey.ShouldEqual, 123)
	})
}

// TestExecuteAction_Error ExecuteActions 失败时向上返回错误
func TestExecuteAction_Error(t *testing.T) {
	convey.Convey("TestExecuteAction_Error", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockOntologyQuery := mocks.NewMockDrivenOntologyQuery(ctrl)
		mockOperatorIntegration := mocks.NewMockDrivenOperatorIntegration(ctrl)

		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		service := &knActionRecallServiceImpl{
			logger:              mockLogger,
			config:              &config.Config{},
			ontologyQuery:       mockOntologyQuery,
			operatorIntegration: mockOperatorIntegration,
		}

		ctx := context.Background()
		req := &interfaces.KnActionExecuteRequest{KnID: "kn-001", AtID: "at-001"}

		mockOntologyQuery.EXPECT().ExecuteActions(gomock.Any(), gomock.Any()).
			Return(nil, errors.New("execute actions failed"))

		_, err := service.ExecuteAction(ctx, req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}
