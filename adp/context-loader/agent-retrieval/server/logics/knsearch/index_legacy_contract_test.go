// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// After the instance is recalled and brought back: the local layer will reveal the nodes/messages after the instance is recalled.
// If it is not done (Schema-only, that is, the default path of the existing caller), even the field will not appear in the response.
func TestKnSearch_PassesThroughInstanceNodes(t *testing.T) {
	convey.Convey("TestKnSearch_PassesThroughInstanceNodes", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()

		localResp := &interfaces.KnSearchLocalResponse{
			ObjectTypes:   []*interfaces.KnSearchObjectType{},
			RelationTypes: []*interfaces.KnSearchRelationType{},
			ActionTypes:   []*interfaces.KnSearchActionType{},
			Nodes:         []*interfaces.KnSearchNode{{ObjectTypeID: "legacy-node"}},
			Message:       "legacy message",
		}
		fakeLocal := &fakeLocalSearch{resp: localResp, err: nil}

		service := &knSearchService{
			Logger:      mockLogger,
			LocalSearch: fakeLocal,
		}

		resp, err := service.KnSearch(context.Background(), &interfaces.KnSearchReq{
			Query: "测试查询",
			KnID:  "kn-001",
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp, convey.ShouldNotBeNil)
		convey.So(resp.ObjectTypes, convey.ShouldResemble, localResp.ObjectTypes)
		convey.So(resp.Nodes, convey.ShouldResemble, localResp.Nodes)
		convey.So(resp.Message, convey.ShouldNotBeNil)
		convey.So(*resp.Message, convey.ShouldEqual, localResp.Message)

		convey.Convey("Schema-only 结果不带 nodes / message", func() {
			schemaOnly := &interfaces.KnSearchLocalResponse{
				ObjectTypes:   []*interfaces.KnSearchObjectType{},
				RelationTypes: []*interfaces.KnSearchRelationType{},
				ActionTypes:   []*interfaces.KnSearchActionType{},
			}
			service.LocalSearch = &fakeLocalSearch{resp: schemaOnly, err: nil}

			resp, err := service.KnSearch(context.Background(), &interfaces.KnSearchReq{
				Query: "测试查询",
				KnID:  "kn-001",
			})
			convey.So(err, convey.ShouldBeNil)
			convey.So(resp.Nodes, convey.ShouldBeNil)
			convey.So(resp.Message, convey.ShouldBeNil)
		})
	})
}

// only_schema is handled by true by default: the caller of the existing kn_search will not suddenly recall additional instances.
func TestKnSearchReqToLocal_OnlySchemaDefaultsToTrue(t *testing.T) {
	convey.Convey("TestKnSearchReqToLocal_OnlySchemaDefaultsToTrue", t, func() {
		local := KnSearchReqToLocal(&interfaces.KnSearchReq{Query: "q", KnID: "kn-001"})
		convey.So(local.OnlySchema, convey.ShouldBeTrue)

		wantInstances := false
		local = KnSearchReqToLocal(&interfaces.KnSearchReq{Query: "q", KnID: "kn-001", OnlySchema: &wantInstances})
		convey.So(local.OnlySchema, convey.ShouldBeFalse)
	})
}
