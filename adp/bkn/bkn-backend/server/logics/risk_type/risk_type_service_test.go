// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package risk_type

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestRiskTypeServiceSearchRiskTypesContinuesDefaultCursorPaging(t *testing.T) {
	Convey("SearchRiskTypes continues default cursor paging after a full page\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		vba := bmock.NewMockVegaBackendAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		service := &riskTypeService{
			appSetting: &common.AppSetting{},
			vba:        vba,
			ps:         ps,
		}
		query := &interfaces.ConceptsQuery{
			KNID:   "kn1",
			Branch: interfaces.MAIN_BRANCH,
		}

		fullPage := make([]map[string]any, interfaces.ConceptQueryLimit)
		for i := range fullPage {
			fullPage[i] = map[string]any{"id": "risk", "name": "risk"}
		}

		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		nextCursor := "cursor-1"
		gomock.InOrder(
			vba.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Mode: "cursor", Limit: interfaces.ConceptQueryLimit})
					So(params.Sort, ShouldResemble, []*interfaces.SortParams{{Field: "id", Direction: "asc"}})
					return &interfaces.DatasetQueryResponse{Entries: fullPage, Paging: &interfaces.ResourceDataPagingResult{NextCursor: &nextCursor}}, nil
				}),
			vba.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Cursor: nextCursor})
					return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"id": "risk-last", "name": "risk-last"}}}, nil
				}),
		)

		result, err := service.SearchRiskTypes(ctx, query)
		So(err, ShouldBeNil)
		So(len(result.Entries), ShouldEqual, interfaces.ConceptQueryLimit+1)
		So(result.Entries[len(result.Entries)-1].RTID, ShouldEqual, "risk-last")
	})
}

func TestRiskTypeServiceHandleImportModeLocalizesInvalidMode(t *testing.T) {
	Convey("Invalid risk type import modes use the request locale\n", t, func() {
		service := &riskTypeService{}

		Convey("Chinese request uses the Chinese catalog", func() {
			_, _, err := service.handleImportMode(
				rest.WithLanguage(context.Background(), rest.SimplifiedChinese),
				"unsupported",
				nil,
			)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.BaseError.ErrorDetails, ShouldEqual, "import_mode \u5fc5\u987b\u4e3a overwrite\u3001normal \u6216 ignore\u3002")
		})

		Convey("English request uses the English catalog", func() {
			_, _, err := service.handleImportMode(
				rest.WithLanguage(context.Background(), rest.AmericanEnglish),
				"unsupported",
				nil,
			)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.BaseError.ErrorDetails, ShouldEqual, "import_mode must be one of overwrite, normal, or ignore.")
		})
	})
}
