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

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestRiskTypeServiceSearchRiskTypesContinuesOffsetPaging(t *testing.T) {
	Convey("SearchRiskTypes continues single paging after a full page\n", t, func() {
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
		gomock.InOrder(
			vba.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Mode: "single", Offset: 0, Limit: interfaces.ConceptQueryLimit})
					return &interfaces.DatasetQueryResponse{Entries: fullPage}, nil
				}),
			vba.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Mode: "single", Offset: interfaces.ConceptQueryLimit, Limit: interfaces.ConceptQueryLimit})
					return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"id": "risk-last", "name": "risk-last"}}}, nil
				}),
		)

		result, err := service.SearchRiskTypes(ctx, query)
		So(err, ShouldBeNil)
		So(len(result.Entries), ShouldEqual, interfaces.ConceptQueryLimit+1)
		So(result.Entries[len(result.Entries)-1].RTID, ShouldEqual, "risk-last")
	})
}
