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

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// TestListKNCapabilities_ReadsTheWholeListNotAPage pins the disable-paging parameter.
//
// bkn-backend defaults this endpoint to ten rows. A page of the bindings is not a scope: the
// eleventh mounted tool would be missing from search_capabilities and refused by execute_tool as
// "not mounted", and nothing in either answer would point at paging.
func TestListKNCapabilities_ReadsTheWholeListNotAPage(t *testing.T) {
	convey.Convey("读绑定列表时关闭分页", t, func() {
		client, mockHTTP, ctrl := newMetricsTestClient(t)
		defer ctrl.Finish()

		var gotURL string
		var gotQuery url.Values
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, src string, q url.Values, _ map[string]string) (int, []byte, error) {
				gotURL, gotQuery = src, q
				return http.StatusOK, []byte(`{"entries":[
					{"capability_type":"function","box_id":"box-1","capability_id":"tool-1"}
				],"total_count":1}`), nil
			})

		refs, err := client.ListKNCapabilities(context.Background(), "kn1", "main",
			interfaces.CapabilityTypeFunction)

		convey.So(err, convey.ShouldBeNil)
		convey.So(len(refs), convey.ShouldEqual, 1)
		convey.So(gotURL, convey.ShouldEndWith, "/in/v1/knowledge-networks/kn1/capabilities")
		convey.So(gotQuery.Get("limit"), convey.ShouldEqual, "-1")
		convey.So(gotQuery.Get("branch"), convey.ShouldEqual, "main")
		convey.So(gotQuery.Get("type"), convey.ShouldEqual, "function")
	})
}

// TestListKNCapabilities_EmptyIsEmptyNotEverything keeps the fail-closed reading at the boundary:
// a network that mounted nothing must come back as an empty scope.
func TestListKNCapabilities_EmptyIsEmptyNotEverything(t *testing.T) {
	convey.Convey("未挂载时返回空切片而非错误", t, func() {
		client, mockHTTP, ctrl := newMetricsTestClient(t)
		defer ctrl.Finish()

		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusOK, []byte(`{"entries":[],"total_count":0}`), nil)

		refs, err := client.ListKNCapabilities(context.Background(), "kn1", "", "")

		convey.So(err, convey.ShouldBeNil)
		convey.So(refs, convey.ShouldNotBeNil)
		convey.So(len(refs), convey.ShouldEqual, 0)
	})
}
