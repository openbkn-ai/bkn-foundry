// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// metric_id 是 agent 自由填写的字符串。未转义就拼进 path 的话，一个带 "?" 的值
// 就能给下游塞进 branch / fill_null 等查询参数——下游按 path 路由仍会命中，
// 只是参数被调用方接管了。
func TestQueryMetricData_EscapesIDsIntoPath(t *testing.T) {
	convey.Convey("kn_id / metric_id 转义后才进 URL", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockHTTP := mocks.NewMockHTTPClient(ctrl)

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://ontology-query:13018/api/ontology-query",
			httpClient: mockHTTP,
		}

		var got string
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, any, error) {
				got = target
				return 200, map[string]any{"datas": []any{}}, nil
			})

		_, err := client.QueryMetricData(context.Background(), "kn1",
			"m1/data?branch=dev&x=", false, &interfaces.MetricQueryDownstreamReq{})
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		// 注入的 branch 没能进查询串，fill_null 仍是本层给的值
		convey.So(parsed.Query().Get("branch"), convey.ShouldBeEmpty)
		convey.So(parsed.Query().Get("fill_null"), convey.ShouldEqual, "false")
		// 注入串整体留在 path 段里，下游只会把它当作一个不存在的 metric_id
		convey.So(parsed.EscapedPath(), convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/metrics/m1%2Fdata%3Fbranch=dev&x=/data")
		convey.So(strings.Count(got, "?"), convey.ShouldEqual, 1)
	})
}

func TestQueryMetricData_ForwardsFillNull(t *testing.T) {
	convey.Convey("fill_null 走查询串透传", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockHTTP := mocks.NewMockHTTPClient(ctrl)
		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://ontology-query:13018/api/ontology-query",
			httpClient: mockHTTP,
		}

		var got string
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, any, error) {
				got = target
				return 200, map[string]any{"datas": []any{}}, nil
			})

		_, err := client.QueryMetricData(context.Background(), "kn1", "m1", true, nil)
		convey.So(err, convey.ShouldBeNil)
		convey.So(got, convey.ShouldEndWith,
			"/in/v1/knowledge-networks/kn1/metrics/m1/data?fill_null=true")
	})
}
