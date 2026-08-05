// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func newMetricsTestClient(t *testing.T) (*bknBackendAccess, *mocks.MockHTTPClient, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTP := mocks.NewMockHTTPClient(ctrl)
	return &bknBackendAccess{
		logger:     mockLogger,
		baseURL:    "http://localhost:8080/api/bkn-backend",
		httpClient: mockHTTP,
	}, mockHTTP, ctrl
}

func TestListMetricsByObjectTypes_QueriesMetricRegistryByScope(t *testing.T) {
	convey.Convey("ListMetricsByObjectTypes 走指标注册表并按 scope 批量过滤", t, func() {
		client, mockHTTP, ctrl := newMetricsTestClient(t)
		defer ctrl.Finish()

		var gotURL string
		var gotQuery url.Values
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, src string, q url.Values, _ map[string]string) (int, []byte, error) {
				gotURL, gotQuery = src, q
				return http.StatusOK, []byte(`{"entries":[
					{"id":"m1","name":"产品总数","comment":"c","unit":"个","unit_type":"count",
					 "metric_type":"atomic","scope_type":"object_type","scope_ref":"ot1",
					 "time_dimension":{"property":"stat_date"},
					 "analysis_dimensions":[{"name":"region"},{"name":""}]},
					{"id":"m2","name":"物料总数","scope_type":"object_type","scope_ref":"ot2"}
				],"total_count":2}`), nil
			})

		metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1", " ot2 ", "ot1", ""})

		convey.So(err, convey.ShouldBeNil)
		convey.So(gotURL, convey.ShouldEqual, "http://localhost:8080/api/bkn-backend/in/v1/knowledge-networks/kn1/metrics")
		convey.So(gotQuery.Get("scope_type"), convey.ShouldEqual, "object_type")
		convey.So(gotQuery.Get("scope_ref"), convey.ShouldEqual, "ot1,ot2")
		convey.So(len(metrics), convey.ShouldEqual, 2)
		convey.So(metrics[0].ID, convey.ShouldEqual, "m1")
		convey.So(metrics[0].TimeDimension, convey.ShouldEqual, "stat_date")
		convey.So(metrics[0].AnalysisDimensions, convey.ShouldResemble, []string{"region"})
		convey.So(metrics[1].ScopeRef, convey.ShouldEqual, "ot2")
	})
}

func TestListMetricsByObjectTypes_DropsMetricsOutsideRequestedScope(t *testing.T) {
	convey.Convey("后端若忽略 scope_ref 过滤，本地仍不把别的对象类的指标塞回来", t, func() {
		client, mockHTTP, ctrl := newMetricsTestClient(t)
		defer ctrl.Finish()

		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusOK, []byte(`{"entries":[
				{"id":"m1","name":"要的","scope_ref":"ot1"},
				{"id":"m9","name":"别人的","scope_ref":"ot9"}
			]}`), nil)

		metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1"})

		convey.So(err, convey.ShouldBeNil)
		convey.So(len(metrics), convey.ShouldEqual, 1)
		convey.So(metrics[0].ID, convey.ShouldEqual, "m1")
	})
}

func TestListMetricsByObjectTypes_NoRequestWithoutIDs(t *testing.T) {
	convey.Convey("kn_id 或对象类 id 为空时不发请求", t, func() {
		client, mockHTTP, ctrl := newMetricsTestClient(t)
		defer ctrl.Finish()

		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{" ", ""})
		convey.So(err, convey.ShouldBeNil)
		convey.So(metrics, convey.ShouldBeNil)

		metrics, err = client.ListMetricsByObjectTypes(context.Background(), "", []string{"ot1"})
		convey.So(err, convey.ShouldBeNil)
		convey.So(metrics, convey.ShouldBeNil)
	})
}

func TestListMetricsByObjectTypes_SurfacesBackendError(t *testing.T) {
	convey.Convey("后端 4xx 带上错误码回给调用方", t, func() {
		client, mockHTTP, ctrl := newMetricsTestClient(t)
		defer ctrl.Finish()

		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusForbidden, []byte(`{"ErrorCode":"BknBackend.Forbidden","Description":"no permission"}`), nil)

		metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1"})
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(metrics, convey.ShouldBeNil)
	})
}
