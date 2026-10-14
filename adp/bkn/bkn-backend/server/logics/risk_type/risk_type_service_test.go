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
	"errors"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

func TestRiskTypeServiceCreateRiskTypesUsesBatchAccess(t *testing.T) {
	newService := func(t *testing.T) (*riskTypeService, sqlmock.Sqlmock, *bmock.MockRiskTypeAccess,
		*bmock.MockPermissionService, *bmock.MockVegaBackendService) {
		t.Helper()
		ctrl := gomock.NewController(t)
		db, dbMock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		rta := bmock.NewMockRiskTypeAccess(ctrl)
		ps := bmock.NewMockPermissionService(ctrl)
		vbs := bmock.NewMockVegaBackendService(ctrl)
		return &riskTypeService{appSetting: &common.AppSetting{}, db: db, rta: rta, ps: ps, vbs: vbs},
			dbMock, rta, ps, vbs
	}
	newRiskTypes := func() []*interfaces.RiskType {
		return []*interfaces.RiskType{
			{RTID: "risk-1", RTName: "Risk 1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
			{RTID: "risk-2", RTName: "Risk 2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
		}
	}

	t.Run("creates multiple risk types through batch access", func(t *testing.T) {
		service, dbMock, rta, ps, vbs := newService(t)
		riskTypes := newRiskTypes()
		ctx := permission.WithKNImportPermissionPrechecked(context.Background())
		dbMock.ExpectBegin()
		rta.EXPECT().GetRiskTypeIdentitiesByIDsOrNames(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
			[]string{"risk-1", "risk-2"}, []string{"Risk 1", "Risk 2"}).Return(nil, nil)
		rta.EXPECT().CreateRiskTypes(gomock.Any(), gomock.Any(), riskTypes).Return(nil)
		ps.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			interfaces.RESOURCE_TYPE_KN, []interfaces.PermissionResourceParent{
				{ResourceID: "kn-1/risk-1", ParentID: "kn-1"},
				{ResourceID: "kn-1/risk-2", ParentID: "kn-1"},
			}).Return(nil)
		vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).
			Return(nil).Times(2)
		dbMock.ExpectCommit()

		ids, err := service.CreateRiskTypes(ctx, nil, riskTypes, interfaces.ImportMode_Normal)

		if err != nil {
			t.Fatalf("CreateRiskTypes() error = %v", err)
		}
		if !reflect.DeepEqual(ids, []string{"risk-1", "risk-2"}) {
			t.Fatalf("CreateRiskTypes() ids = %#v", ids)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})

	t.Run("overwrites multiple risk types through batch access", func(t *testing.T) {
		service, dbMock, rta, ps, vbs := newService(t)
		riskTypes := newRiskTypes()
		ctx := permission.WithKNImportPermissionPrechecked(context.Background())
		dbMock.ExpectBegin()
		rta.EXPECT().GetRiskTypeIdentitiesByIDsOrNames(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
			[]string{"risk-1", "risk-2"}, []string{"Risk 1", "Risk 2"}).Return([]*interfaces.RiskType{
			{RTID: "risk-1", RTName: "Risk 1"},
			{RTID: "risk-2", RTName: "Risk 2"},
		}, nil)
		ps.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			interfaces.RESOURCE_TYPE_KN, []interfaces.PermissionResourceParent{}).Return(nil)
		rta.EXPECT().UpdateRiskTypes(gomock.Any(), gomock.Any(), riskTypes).Return(nil)
		vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).
			Return(nil).Times(2)
		dbMock.ExpectCommit()

		ids, err := service.CreateRiskTypes(ctx, nil, riskTypes, interfaces.ImportMode_Overwrite)

		if err != nil {
			t.Fatalf("CreateRiskTypes() error = %v", err)
		}
		if !reflect.DeepEqual(ids, []string{"risk-1", "risk-2"}) {
			t.Fatalf("CreateRiskTypes() ids = %#v", ids)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})

	t.Run("maps a batch name race to the existing domain error", func(t *testing.T) {
		service, dbMock, rta, _, _ := newService(t)
		riskTypes := newRiskTypes()
		ctx := permission.WithKNImportPermissionPrechecked(context.Background())
		dbMock.ExpectBegin()
		rta.EXPECT().GetRiskTypeIdentitiesByIDsOrNames(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
			[]string{"risk-1", "risk-2"}, []string{"Risk 1", "Risk 2"}).Return(nil, nil)
		rta.EXPECT().CreateRiskTypes(gomock.Any(), gomock.Any(), riskTypes).Return(
			errors.New("Error 1062 (23000): Duplicate entry 'kn-1-main-Risk 2' for key 'uk_risk_type_name'"))
		dbMock.ExpectRollback()

		ids, err := service.CreateRiskTypes(ctx, nil, riskTypes, interfaces.ImportMode_Normal)

		if len(ids) != 0 || err == nil {
			t.Fatalf("CreateRiskTypes() = (%v, %v), want empty ids and an error", ids, err)
		}
		httpErr, ok := err.(*rest.HTTPError)
		if !ok || httpErr.BaseError.ErrorCode != berrors.BknBackend_RiskType_RiskTypeNameExisted {
			t.Fatalf("CreateRiskTypes() error = %#v, want %s", err,
				berrors.BknBackend_RiskType_RiskTypeNameExisted)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})
}

func TestRiskTypeServiceCreateRiskTypesResourceParentLifecycle(t *testing.T) {
	newRiskType := func() []*interfaces.RiskType {
		return []*interfaces.RiskType{{
			RTID:   "risk-1",
			RTName: "Risk 1",
			KNID:   "kn-1",
			Branch: interfaces.MAIN_BRANCH,
		}}
	}

	t.Run("rolls back when the parent edge cannot be written", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		db, dbMock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		defer func() { _ = db.Close() }()

		rta := bmock.NewMockRiskTypeAccess(ctrl)
		ps := bmock.NewMockPermissionService(ctrl)
		vbs := bmock.NewMockVegaBackendService(ctrl)
		service := &riskTypeService{appSetting: &common.AppSetting{}, db: db, rta: rta, ps: ps, vbs: vbs}
		parentErr := errors.New("parent edge write failed")
		parentItems := []interfaces.PermissionResourceParent{{ResourceID: "kn-1/risk-1", ParentID: "kn-1"}}

		dbMock.ExpectBegin()
		ps.EXPECT().CheckPermission(gomock.Any(),
			interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: "kn-1"},
			[]string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		rta.EXPECT().CheckRiskTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "risk-1").
			Return("", false, nil)
		rta.EXPECT().CheckRiskTypeExistByName(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "Risk 1").
			Return("", false, nil)
		rta.EXPECT().CreateRiskType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ps.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			interfaces.RESOURCE_TYPE_KN, parentItems).Return(parentErr)
		dbMock.ExpectRollback()

		_, err = service.CreateRiskTypes(context.Background(), nil, newRiskType(), interfaces.ImportMode_Normal)
		if !errors.Is(err, parentErr) {
			t.Fatalf("CreateRiskTypes() error = %v, want %v", err, parentErr)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})

	t.Run("removes the parent edge when commit fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		db, dbMock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		defer func() { _ = db.Close() }()

		rta := bmock.NewMockRiskTypeAccess(ctrl)
		ps := bmock.NewMockPermissionService(ctrl)
		vbs := bmock.NewMockVegaBackendService(ctrl)
		service := &riskTypeService{appSetting: &common.AppSetting{}, db: db, rta: rta, ps: ps, vbs: vbs}
		commitErr := errors.New("commit failed")
		parentItems := []interfaces.PermissionResourceParent{{ResourceID: "kn-1/risk-1", ParentID: "kn-1"}}

		dbMock.ExpectBegin()
		ps.EXPECT().CheckPermission(gomock.Any(),
			interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: "kn-1"},
			[]string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		rta.EXPECT().CheckRiskTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "risk-1").
			Return("", false, nil)
		rta.EXPECT().CheckRiskTypeExistByName(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "Risk 1").
			Return("", false, nil)
		rta.EXPECT().CreateRiskType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ps.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			interfaces.RESOURCE_TYPE_KN, parentItems).Return(nil)
		vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil)
		dbMock.ExpectCommit().WillReturnError(commitErr)
		ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			[]string{"kn-1/risk-1"}).Return(nil)

		_, err = service.CreateRiskTypes(context.Background(), nil, newRiskType(), interfaces.ImportMode_Normal)
		if !errors.Is(err, commitErr) {
			t.Fatalf("CreateRiskTypes() error = %v, want %v", err, commitErr)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})
}

func TestRiskTypeServiceDeleteRiskTypesAuthorizationCleanup(t *testing.T) {
	newService := func(t *testing.T) (*riskTypeService, sqlmock.Sqlmock, *bmock.MockRiskTypeAccess,
		*bmock.MockPermissionService, *bmock.MockVegaBackendService) {
		t.Helper()
		ctrl := gomock.NewController(t)
		db, dbMock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		rta := bmock.NewMockRiskTypeAccess(ctrl)
		ps := bmock.NewMockPermissionService(ctrl)
		vbs := bmock.NewMockVegaBackendService(ctrl)
		return &riskTypeService{appSetting: &common.AppSetting{}, db: db, rta: rta, ps: ps, vbs: vbs},
			dbMock, rta, ps, vbs
	}
	prepareDelete := func(dbMock sqlmock.Sqlmock, rta *bmock.MockRiskTypeAccess,
		ps *bmock.MockPermissionService, vbs *bmock.MockVegaBackendService) {
		dbMock.ExpectBegin()
		rta.EXPECT().CheckRiskTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "risk-1").
			Return("Risk 1", true, nil)
		ps.EXPECT().CheckPermission(gomock.Any(),
			interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_RISK_TYPE, ID: "kn-1/risk-1"},
			[]string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
		rta.EXPECT().DeleteRiskTypesByIDs(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
			[]string{"risk-1"}).Return(int64(1), nil)
		vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).Return(nil)
	}

	t.Run("does not clean Safe when commit fails", func(t *testing.T) {
		service, dbMock, rta, ps, vbs := newService(t)
		prepareDelete(dbMock, rta, ps, vbs)
		commitErr := errors.New("commit failed")
		dbMock.ExpectCommit().WillReturnError(commitErr)

		err := service.DeleteRiskTypesByIDs(context.Background(), nil, "kn-1", interfaces.MAIN_BRANCH,
			[]string{"risk-1"})
		if !errors.Is(err, commitErr) {
			t.Fatalf("DeleteRiskTypesByIDs() error = %v, want %v", err, commitErr)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})

	t.Run("cleans policies and parent edges after commit", func(t *testing.T) {
		service, dbMock, rta, ps, vbs := newService(t)
		prepareDelete(dbMock, rta, ps, vbs)
		dbMock.ExpectCommit()
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			[]string{"kn-1/risk-1"}).Return(nil)
		ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			[]string{"kn-1/risk-1"}).Return(nil)

		err := service.DeleteRiskTypesByIDs(context.Background(), nil, "kn-1", interfaces.MAIN_BRANCH,
			[]string{"risk-1"})
		if err != nil {
			t.Fatalf("DeleteRiskTypesByIDs() error = %v", err)
		}
		if err := dbMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("database expectations were not met: %v", err)
		}
	})
}

func TestRiskTypeServiceSearchRiskTypesContinuesDefaultCursorPaging(t *testing.T) {
	Convey("SearchRiskTypes continues default cursor paging after a full page\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		rta := bmock.NewMockRiskTypeAccess(mockCtrl)
		service := &riskTypeService{
			appSetting: &common.AppSetting{},
			vbs:        vbs,
			ps:         ps,
			rta:        rta,
		}
		query := &interfaces.ConceptsQuery{
			KNID:   "kn1",
			Branch: interfaces.MAIN_BRANCH,
		}

		fullPage := make([]map[string]any, interfaces.ConceptQueryLimit)
		for i := range fullPage {
			fullPage[i] = map[string]any{"id": "risk", "name": "risk"}
		}

		rta.EXPECT().GetAllRiskTypesByKnID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return([]*interfaces.RiskType{
			{RTID: "risk"}, {RTID: "risk-last"},
		}, nil)
		ps.EXPECT().FilterVisibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
			[]string{"kn1/risk", "kn1/risk-last"}, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"kn1/risk":      {ResourceID: "kn1/risk"},
				"kn1/risk-last": {ResourceID: "kn1/risk-last"},
			}, nil)
		nextCursor := "cursor-1"
		gomock.InOrder(
			vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Mode: "cursor", Limit: interfaces.ConceptQueryLimit})
					So(params.Sort, ShouldResemble, []*interfaces.SortParams{{Field: "id", Direction: "asc"}})
					return &interfaces.DatasetQueryResponse{Entries: fullPage, Paging: &interfaces.ResourceDataPagingResult{NextCursor: &nextCursor}}, nil
				}),
			vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
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

func TestRiskTypeServiceSearchRiskTypesFiltersTrustedCandidatesBeforeDatasetPaging(t *testing.T) {
	ctrl := gomock.NewController(t)
	rta := bmock.NewMockRiskTypeAccess(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	service := &riskTypeService{appSetting: &common.AppSetting{}, rta: rta, vbs: vbs, ps: ps}

	rta.EXPECT().GetAllRiskTypesByKnID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).
		Return([]*interfaces.RiskType{{RTID: "risk-1"}, {RTID: "risk-2"}, {RTID: "risk-3"}}, nil)
	ps.EXPECT().FilterVisibleResources(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
		[]string{"kn-1/risk-1", "kn-1/risk-2", "kn-1/risk-3"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).
		Return(map[string]interfaces.PermissionResourceOps{
			"kn-1/risk-1": {ResourceID: "kn-1/risk-1"},
			"kn-1/risk-3": {ResourceID: "kn-1/risk-3"},
		}, nil)

	wantFilter := map[string]any{
		"field": "id", "operation": "in", "value": []string{"risk-1", "risk-3"}, "value_from": "const",
	}
	nextCursor := "visible-cursor"
	gomock.InOrder(
		vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
				if !reflect.DeepEqual(params.FilterCondition, wantFilter) || !params.NeedTotal {
					t.Fatalf("total query params = %#v", params)
				}
				return &interfaces.DatasetQueryResponse{TotalCount: 2}, nil
			}),
		vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
				if !reflect.DeepEqual(params.FilterCondition, wantFilter) {
					t.Fatalf("page query filter = %#v", params.FilterCondition)
				}
				return &interfaces.DatasetQueryResponse{
					Entries: []map[string]any{{"id": "risk-1", "name": "Risk 1"}},
					Paging:  &interfaces.ResourceDataPagingResult{NextCursor: &nextCursor},
				}, nil
			}),
	)

	result, err := service.SearchRiskTypes(context.Background(), &interfaces.ConceptsQuery{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, NeedTotal: true, Limit: 1,
	})
	if err != nil {
		t.Fatalf("SearchRiskTypes() error = %v", err)
	}
	if result.TotalCount != 2 || len(result.Entries) != 1 || result.Entries[0].RTID != "risk-1" ||
		result.NextCursor == nil || *result.NextCursor != nextCursor {
		t.Fatalf("SearchRiskTypes() = %#v", result)
	}
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
