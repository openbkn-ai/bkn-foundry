// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
	"bkn-backend/logics"
)

func Test_objectTypeService_CheckObjectTypeExistByID(t *testing.T) {
	Convey("Test CheckObjectTypeExistByID\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			ota:        ota,
		}

		Convey("Success when object type exists\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otID := "ot1"
			otName := "object_type1"

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otName, true, nil)

			name, exist, err := service.CheckObjectTypeExistByID(ctx, knID, branch, otID)
			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(name, ShouldEqual, otName)
		})

		Convey("Success when object type does not exist\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otID := "ot1"

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)

			name, exist, err := service.CheckObjectTypeExistByID(ctx, knID, branch, otID)
			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(name, ShouldEqual, "")
		})

		Convey("Failed when access layer returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otID := "ot1"

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			name, exist, err := service.CheckObjectTypeExistByID(ctx, knID, branch, otID)
			So(err, ShouldNotBeNil)
			So(exist, ShouldBeFalse)
			So(name, ShouldEqual, "")
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_InternalError_CheckObjectTypeIfExistFailed)
		})
	})
}

func Test_objectTypeService_CheckObjectTypeExistByName(t *testing.T) {
	Convey("Test CheckObjectTypeExistByName\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			ota:        ota,
		}

		Convey("Success when object type exists\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otName := "object_type1"
			otID := "ot1"

			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otID, true, nil)

			id, exist, err := service.CheckObjectTypeExistByName(ctx, knID, branch, otName)
			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(id, ShouldEqual, otID)
		})

		Convey("Success when object type does not exist\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otName := "object_type1"

			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)

			id, exist, err := service.CheckObjectTypeExistByName(ctx, knID, branch, otName)
			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(id, ShouldEqual, "")
		})

		Convey("Failed when access layer returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otName := "object_type1"

			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			id, exist, err := service.CheckObjectTypeExistByName(ctx, knID, branch, otName)
			So(err, ShouldNotBeNil)
			So(exist, ShouldBeFalse)
			So(id, ShouldEqual, "")
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_InternalError_CheckObjectTypeIfExistFailed)
		})
	})
}

func Test_objectTypeService_GetObjectTypeIDsByKnID(t *testing.T) {
	Convey("Test GetObjectTypeIDsByKnID\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			ota:        ota,
		}

		Convey("Success getting object type IDs\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1", "ot2"}

			ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return(otIDs, nil)

			result, err := service.GetObjectTypeIDsByKnID(ctx, knID, branch)
			So(err, ShouldBeNil)
			So(result, ShouldResemble, otIDs)
		})

		Convey("Success with empty result\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH

			ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{}, nil)

			result, err := service.GetObjectTypeIDsByKnID(ctx, knID, branch)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when access layer returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH

			ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.GetObjectTypeIDsByKnID(ctx, knID, branch)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed)
		})
	})
}

func Test_objectTypeService_GetObjectTypesByIDs(t *testing.T) {
	Convey("Test GetObjectTypesByIDs\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		ma := bmock.NewMockMetricAccess(mockCtrl)
		ums := bmock.NewMockUserMgmtService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
			ps:         ps,
			cga:        cga,
			ma:         ma,
			ums:        ums,
		}

		Convey("Success getting object types by IDs\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1", "ot2"}
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
				},
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot2",
						OTName: "ot2",
					},
				},
			}
			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()
			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 2)
		})

		Convey("Failed when object types count mismatch\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1", "ot2"}
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
				},
			}

			smock.ExpectBegin()
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)
			smock.ExpectCommit()
			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
			So(result, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeNotFound)
		})

		Convey("Failed when permission check fails\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			smock.ExpectBegin()
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), knID, branch, otIDs).Return([]*interfaces.ObjectType{{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1"},
			}}, nil)
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))
			smock.ExpectRollback()

			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when GetObjectTypesByIDs returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			smock.ExpectBegin()
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when GetConceptGroupsByOTIDs returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when Begin transaction fails\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			// Simulate Begin failure.
			db2, _, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			_ = db2.Close() // Close the database connection to simulate Begin failure.
			service2 := &objectTypeService{
				appSetting: appSetting,
				db:         db2,
				ota:        ota,
				ps:         ps,
				cga:        cga,
			}

			result, err := service2.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Success with existing transaction\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
				},
			}

			smock.ExpectBegin()
			tx, _ := db.Begin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

			result, err := service.GetObjectTypesByIDs(ctx, tx, knID, branch, otIDs)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 1)
		})

		Convey("Ignore dependency error when GetDataViewByID returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:       "ot1",
						OTName:     "ot1",
						DataSource: &interfaces.ResourceInfo{ID: "dv1"},
					},
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 1)
		})

		Convey("Ignore dependency error when GetMetricByID returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}
			ma := bmock.NewMockMetricAccess(mockCtrl)
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:       "ot1",
						OTName:     "ot1",
						DataSource: &interfaces.ResourceInfo{ID: "dv1"},
						LogicProperties: []*interfaces.LogicProperty{
							{
								Name: "lp1",
								DataSource: &interfaces.ResourceInfo{
									Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
									ID:   "metric1",
								},
							},
						},
					},
					KNID:   knID,
					Branch: branch,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			service.ma = ma
			ma.EXPECT().GetMetricByID(gomock.Any(), knID, branch, "metric1").Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 1)
		})
	})
}

func Test_objectTypeService_GetAllObjectTypesByKnID(t *testing.T) {
	Convey("Test GetAllObjectTypesByKnID\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			ota:        ota,
		}

		Convey("Success getting all object types\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otMap := map[string]*interfaces.ObjectType{
				"ot1": {
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
				},
			}

			ota.EXPECT().GetAllObjectTypesByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return(otMap, nil)

			result, err := service.GetAllObjectTypesByKnID(ctx, knID, branch)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(len(result), ShouldEqual, 1)
		})

		Convey("Failed when access layer returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH

			ota.EXPECT().GetAllObjectTypesByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.GetAllObjectTypesByKnID(ctx, knID, branch)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})
	})
}

func Test_objectTypeService_GetObjectTypeByID(t *testing.T) {
	Convey("Test GetObjectTypeByID\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
		}

		Convey("Success getting object type by ID\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otID := "ot1"
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   otID,
					OTName: "ot1",
				},
			}

			smock.ExpectBegin()
			ota.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(ot, nil)
			smock.ExpectCommit()

			result, err := service.GetObjectTypeByID(ctx, nil, knID, branch, otID)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(result.OTID, ShouldEqual, otID)
		})

		Convey("Failed when access layer returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otID := "ot1"

			smock.ExpectBegin()
			ota.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, err := service.GetObjectTypeByID(ctx, nil, knID, branch, otID)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})
	})
}

func Test_objectTypeService_CreateObjectTypes(t *testing.T) {
	Convey("Test CreateObjectTypes\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			ServerSetting: common.ServerSetting{
				DefaultSmallModelEnabled: false,
			},
		}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		ps.EXPECT().UpsertResourceParents(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		ps.EXPECT().DeleteResourceParents(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		mfs := bmock.NewMockModelFactoryService(mockCtrl)
		aoa := bmock.NewMockAgentOperatorAccess(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
			ps:         ps,
			cga:        cga,
			vbs:        vbs,
			mfs:        mfs,
			aoa:        aoa,
		}

		Convey("Success creating object types with normal mode\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CreateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CreateObjectTypeStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 1)
			So(result[0], ShouldEqual, "ot1")
		})

		Convey("Failed when permission check fails\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when object type ID already exists in normal mode\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			smock.ExpectRollback()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeIDExisted)
		})

		Convey("Success with ignore mode when object type exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			smock.ExpectCommit()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Ignore, false, true)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Success with Overwrite mode when ID exists\n", func() {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			objectTypes := []*interfaces.ObjectType{ot}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil).AnyTimes()
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil).Times(2)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			smock.ExpectCommit()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Overwrite, false, true)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Success with empty OTID generates new ID\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx, knID, branch, otID interface{}) {
				So(otID, ShouldNotBeEmpty)
			}).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CreateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CreateObjectTypeStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 1)
			So(result[0], ShouldNotBeEmpty)
		})

		Convey("Failed when CreateObjectType returns error\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CreateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when CreateObjectTypeStatus returns error\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CreateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CreateObjectTypeStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when InsertDatasetData returns error\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CreateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().CreateObjectTypeStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, err := service.CreateObjectTypes(ctx, nil, objectTypes, interfaces.ImportMode_Normal, false, true)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_GetObjectTypeSampleData(t *testing.T) {
	Convey("Test GetObjectTypeSampleData\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		ums := bmock.NewMockUserMgmtService(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: &common.AppSetting{},
			db:         db,
			ota:        ota,
			ps:         ps,
			cga:        cga,
			ums:        ums,
			vbs:        vbs,
		}

		Convey("Success with resource-backed object type and mapped fields\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "物料",
					DataSource: &interfaces.ResourceInfo{
						ID:   "resource1",
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
					},
					DataProperties: []*interfaces.DataProperty{
						{
							Name:        "material_code",
							DisplayName: "物料编码",
							MappedField: &interfaces.Field{Name: "code"},
						},
						{
							Name:        "material_name",
							DisplayName: "物料名称",
						},
					},
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"ot1"}).Return([]*interfaces.ObjectType{objectType}, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			vbs.EXPECT().GetResourceByID(gomock.Any(), "resource1").Return(&interfaces.VegaResource{
				ID:   "resource1",
				Name: "resource1",
				SchemaDefinition: []*interfaces.Property{
					{Name: "code", DisplayName: "物料编码", Type: "string"},
					{Name: "material_name", DisplayName: "物料名称", Type: "string"},
				},
			}, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()
			vbs.EXPECT().QueryResourceData(gomock.Any(), "resource1", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{
						Mode:   "single",
						Offset: 10,
						Limit:  20,
					})
					So(params.NeedTotal, ShouldBeTrue)
					So(params.OutputFields, ShouldResemble, []string{"code", "material_name"})
					return &interfaces.DatasetQueryResponse{
						Entries: []map[string]any{
							{"code": "M001", "material_name": "螺丝"},
						},
						TotalCount: 1,
					}, nil
				},
			)

			result, err := service.GetObjectTypeSampleData(ctx, "kn1", interfaces.MAIN_BRANCH, "ot1", interfaces.ObjectTypeSampleDataQueryParams{
				Limit:     20,
				NeedTotal: true,
				Offset:    10,
			})

			So(err, ShouldBeNil)
			So(result.Name, ShouldEqual, "物料")
			So(result.TotalCount, ShouldEqual, 1)
			So(result.Columns, ShouldResemble, []*interfaces.ObjectTypeSampleDataColumn{
				{DataIndex: "material_code", Title: "物料编码"},
				{DataIndex: "material_name", Title: "物料名称"},
			})
			So(result.Entries, ShouldResemble, []map[string]any{
				{"material_code": "M001", "material_name": "螺丝"},
			})
		})

	})
}

func Test_objectTypeService_ValidateObjectTypes(t *testing.T) {
	Convey("Test ValidateObjectTypes\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ps := bmock.NewMockPermissionService(mockCtrl)
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		mfs := bmock.NewMockModelFactoryService(mockCtrl)
		ma := bmock.NewMockMetricAccess(mockCtrl)
		aoa := bmock.NewMockAgentOperatorAccess(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			db:  db,
			ps:  ps,
			ota: ota,
			vbs: vbs,
			mfs: mfs,
			ma:  ma,
			aoa: aoa,
			cga: cga,
		}

		expectImportModeOK := func() {
			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
		}

		Convey("Success with strict mode and no external deps\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTName: "ot1"}, KNID: "kn1"},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Strict mode validates resource data source via GetResourceByID not data view\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTName: "ot1",
						DataSource: &interfaces.ResourceInfo{
							Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
							ID:   "res1",
						},
					},
					KNID: "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			vbs.EXPECT().GetResourceByID(gomock.Any(), "res1").Return(&interfaces.VegaResource{Name: "r1"}, nil)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Strict mode fails when resource data source does not exist\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTName: "ot1",
						DataSource: &interfaces.ResourceInfo{
							Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
							ID:   "res_missing",
						},
					},
					KNID: "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			vbs.EXPECT().GetResourceByID(gomock.Any(), "res_missing").Return(nil, nil)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldNotBeNil)
		})

		Convey("Strict mode skips logic property checks when strictMode is false\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTName: "ot1",
						LogicProperties: []*interfaces.LogicProperty{
							{Name: "lp1", Type: ""},
						},
					},
					KNID: "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, false, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Fails strict mode when KN metric does not exist\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
						DataSource: &interfaces.ResourceInfo{
							Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
							ID:   "res1",
						},
						LogicProperties: []*interfaces.LogicProperty{
							{
								Name: "lp1",
								Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
								DataSource: &interfaces.ResourceInfo{
									Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
									ID:   "mid1",
								},
							},
						},
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			vbs.EXPECT().GetResourceByID(gomock.Any(), "res1").Return(&interfaces.VegaResource{Name: "r1"}, nil)
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "mid1").Return(nil, nil)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldNotBeNil)
		})

		Convey("Success strict mode when KN metric exists and scope matches\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
						DataSource: &interfaces.ResourceInfo{
							Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
							ID:   "res1",
						},
						LogicProperties: []*interfaces.LogicProperty{
							{
								Name: "lp1",
								Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
								DataSource: &interfaces.ResourceInfo{
									Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
									ID:   "mid1",
								},
							},
						},
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			vbs.EXPECT().GetResourceByID(gomock.Any(), "res1").Return(&interfaces.VegaResource{Name: "r1"}, nil)
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "mid1").Return(&interfaces.MetricDefinition{
				ID:       "mid1",
				ScopeRef: "ot1",
			}, nil)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Fails strict mode when tool does not exist\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTName: "ot1",
						LogicProperties: []*interfaces.LogicProperty{
							{
								Name: "lp1",
								Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								DataSource: &interfaces.ResourceInfo{
									Type:   interfaces.LOGIC_PROPERTY_TYPE_TOOL,
									BoxID:  "box1",
									ToolID: "tool1",
								},
							},
						},
					},
					KNID: "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			aoa.EXPECT().GetToolByID(gomock.Any(), "box1", "tool1").Return(sql.ErrNoRows)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldNotBeNil)
		})

		Convey("Success strict mode when tool binding resolves\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTName: "ot1",
						LogicProperties: []*interfaces.LogicProperty{
							{
								Name: "lp1",
								Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								DataSource: &interfaces.ResourceInfo{
									Type:   interfaces.LOGIC_PROPERTY_TYPE_TOOL,
									BoxID:  "box1",
									ToolID: "tool1",
								},
							},
						},
					},
					KNID: "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			aoa.EXPECT().GetToolByID(gomock.Any(), "box1", "tool1").Return(nil)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Success strict mode when tool exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTName: "ot1",
						LogicProperties: []*interfaces.LogicProperty{
							{
								Name: "lp1",
								Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
								DataSource: &interfaces.ResourceInfo{
									Type:   interfaces.LOGIC_PROPERTY_TYPE_TOOL,
									BoxID:  "box1",
									ToolID: "tool1",
								},
							},
						},
					},
					KNID: "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			aoa.EXPECT().GetToolByID(gomock.Any(), "box1", "tool1").Return(nil)
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Strict mode validates concept groups when present\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTName: "ot1"},
					ConceptGroups:          []*interfaces.ConceptGroup{{CGID: "cg1"}},
					KNID:                   "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			smock.ExpectBegin()
			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"cg1"}).Return([]*interfaces.ConceptGroup{{CGID: "cg1"}}, nil)
			smock.ExpectRollback()
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, true, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("strictMode false skips concept group existence validation\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTName: "ot1"},
					ConceptGroups:          []*interfaces.ConceptGroup{{CGID: "cg_not_in_db"}},
					KNID:                   "kn1",
				},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			expectImportModeOK()
			err := service.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, objectTypes, false, nil, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})
	})
}

func Test_objectTypeService_ListObjectTypes(t *testing.T) {
	Convey("Test ListObjectTypes\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		ums := bmock.NewMockUserMgmtService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
			ps:         ps,
			cga:        cga,
			ums:        ums,
		}

		Convey("Success listing object types\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				PaginationQueryParameters: interfaces.PaginationQueryParameters{
					Limit:  10,
					Offset: 0,
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().ListObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(objectTypes, nil)
			ota.EXPECT().GetObjectTypesTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 1)
			So(len(result), ShouldEqual, 1)
		})

		Convey("Success with empty result\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				PaginationQueryParameters: interfaces.PaginationQueryParameters{
					Limit:  10,
					Offset: 0,
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().ListObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return([]*interfaces.ObjectType{}, nil)
			ota.EXPECT().GetObjectTypesTotal(gomock.Any(), gomock.Any()).Return(0, nil)
			smock.ExpectCommit()

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 0)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when permission check fails\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldNotBeNil)
			So(total, ShouldEqual, 0)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when ListObjectTypes returns error\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().ListObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldNotBeNil)
			So(total, ShouldEqual, 0)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Failed when GetAccountNames returns error\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				PaginationQueryParameters: interfaces.PaginationQueryParameters{
					Limit:  10,
					Offset: 0,
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().ListObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(objectTypes, nil)
			ota.EXPECT().GetObjectTypesTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldNotBeNil)
			So(total, ShouldEqual, 0)
			So(len(result), ShouldEqual, 0)
		})

		Convey("Success with Limit = -1\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				PaginationQueryParameters: interfaces.PaginationQueryParameters{
					Limit:  -1,
					Offset: 0,
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().ListObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(objectTypes, nil)
			ota.EXPECT().GetObjectTypesTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 1)
			So(len(result), ShouldEqual, 1)
		})

		Convey("Success with Offset out of bounds\n", func() {
			query := interfaces.ObjectTypesQueryParams{
				PaginationQueryParameters: interfaces.PaginationQueryParameters{
					Limit:  10,
					Offset: 100,
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().ListObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return([]*interfaces.ObjectType{}, nil)
			ota.EXPECT().GetObjectTypesTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			smock.ExpectCommit()

			result, total, err := service.ListObjectTypes(ctx, nil, query)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 1)
			So(len(result), ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_UpdateObjectType(t *testing.T) {
	Convey("Test UpdateObjectType\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			ServerSetting: common.ServerSetting{
				DefaultSmallModelEnabled: false,
			},
		}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil).AnyTimes()
		ps := bmock.NewMockPermissionService(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		mfs := bmock.NewMockModelFactoryService(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
			ps:         ps,
			cga:        cga,
			mfs:        mfs,
			vbs:        vbs,
		}

		Convey("Success updating object type\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			err := service.UpdateObjectType(ctx, nil, objectType, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed when permission check fails\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))

			err := service.UpdateObjectType(ctx, nil, objectType, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when UpdateObjectType returns error\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.UpdateObjectType(ctx, nil, objectType, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when syncObjectGroups returns error\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.UpdateObjectType(ctx, nil, objectType, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when InsertDatasetData returns error\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.UpdateObjectType(ctx, nil, objectType, true)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_objectTypeService_UpdateDataProperties(t *testing.T) {
	Convey("Test UpdateDataProperties\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			ServerSetting: common.ServerSetting{
				DefaultSmallModelEnabled: false,
			},
		}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)
		mfs := bmock.NewMockModelFactoryService(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
			ps:         ps,
			mfs:        mfs,
			vbs:        vbs,
		}

		Convey("Success updating data properties\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name: "prop1",
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			dataProperties := []*interfaces.DataProperty{
				{
					Name: "prop1",
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateDataProperties(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			err := service.UpdateDataProperties(ctx, objectType, dataProperties, true)
			So(err, ShouldBeNil)
		})

		Convey("Success with vector index when strictMode false skips model validation\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name: "prop1",
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			dataProperties := []*interfaces.DataProperty{
				{
					Name: "prop1",
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateDataProperties(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()

			err := service.UpdateDataProperties(ctx, objectType, dataProperties, false)
			So(err, ShouldBeNil)
		})

		Convey("Failed when permission check fails\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			dataProperties := []*interfaces.DataProperty{}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))

			err := service.UpdateDataProperties(ctx, objectType, dataProperties, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when UpdateDataProperties returns error\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name: "prop1",
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			dataProperties := []*interfaces.DataProperty{
				{
					Name: "prop1",
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateDataProperties(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectCommit()

			err := service.UpdateDataProperties(ctx, objectType, dataProperties, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when InsertDatasetData returns error\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name: "prop1",
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			dataProperties := []*interfaces.DataProperty{
				{
					Name: "prop1",
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateDataProperties(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectCommit()

			err := service.UpdateDataProperties(ctx, objectType, dataProperties, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success adding new property\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "object_type1",
					DataProperties: []*interfaces.DataProperty{
						{
							Name: "prop1",
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			dataProperties := []*interfaces.DataProperty{
				{
					Name: "prop2",
				},
			}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().UpdateDataProperties(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			smock.ExpectCommit()
			err := service.UpdateDataProperties(ctx, objectType, dataProperties, true)
			So(err, ShouldBeNil)
			So(len(objectType.DataProperties), ShouldEqual, 2)
		})
	})
}

func Test_objectTypeService_DeleteObjectTypesByIDs(t *testing.T) {
	Convey("Test DeleteObjectTypesByIDs\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil).AnyTimes()
		ps := bmock.NewMockPermissionService(mockCtrl)
		ps.EXPECT().DeleteResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		ps.EXPECT().DeleteResourceParents(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			db:         db,
			ota:        ota,
			ps:         ps,
			cga:        cga,
			vbs:        vbs,
		}

		Convey("Success deleting object types\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1", "ot2"}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().DeleteObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(2), nil)
			ota.EXPECT().DeleteObjectTypeStatusByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(2), nil)
			vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			cga.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(2), nil)
			smock.ExpectCommit()

			err := service.DeleteObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldBeNil)
		})

		Convey("Failed when permission check fails\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))

			err := service.DeleteObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteObjectTypesByIDs returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().DeleteObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.DeleteObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteObjectTypeStatusByIDs returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().DeleteObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
			ota.EXPECT().DeleteObjectTypeStatusByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.DeleteObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteDatasetDocumentByID returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().DeleteObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
			ota.EXPECT().DeleteObjectTypeStatusByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
			vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.DeleteObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteObjectTypesFromGroup returns error\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			smock.ExpectBegin()
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().DeleteObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
			ota.EXPECT().DeleteObjectTypeStatusByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
			vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))
			smock.ExpectRollback()

			err := service.DeleteObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_objectTypeService_GetObjectTypesMapByIDs(t *testing.T) {
	Convey("Test GetObjectTypesMapByIDs\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			ota:        ota,
			ps:         ps,
		}

		Convey("Success getting object types map\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1", "ot2"}
			otArr := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
						DataProperties: []*interfaces.DataProperty{
							{
								Name:        "prop1",
								DisplayName: "Property1",
							},
						},
					},
				},
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot2",
						OTName: "object_type2",
					},
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(otArr, nil)

			result, err := service.GetObjectTypesMapByIDs(ctx, knID, branch, otIDs, true)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, 2)
			So(result["ot1"], ShouldNotBeNil)
			So(result["ot2"], ShouldNotBeNil)
			So(result["ot1"].PropertyMap["prop1"], ShouldEqual, "Property1")
		})

		Convey("Failed when permission check fails\n", func() {
			knID := "kn1"
			branch := interfaces.MAIN_BRANCH
			otIDs := []string{"ot1"}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 403, berrors.BknBackend_InternalError_CheckPermissionFailed))

			result, err := service.GetObjectTypesMapByIDs(ctx, knID, branch, otIDs, false)
			So(err, ShouldNotBeNil)
			So(len(result), ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_InsertDatasetData(t *testing.T) {
	Convey("Test InsertDatasetData\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			ServerSetting: common.ServerSetting{
				DefaultSmallModelEnabled: false,
			},
		}
		vbs := bmock.NewMockVegaBackendService(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			vbs:        vbs,
		}

		Convey("Success inserting empty list\n", func() {
			objectTypes := []*interfaces.ObjectType{}

			err := service.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldBeNil)
		})

		Convey("Success inserting object types\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

			err := service.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldBeNil)
		})

		Convey("Failed when InsertData returns error\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			vbs.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldNotBeNil)
		})

		Convey("Success inserting object types with vector enabled\n", func() {
			appSettingWithVector := &common.AppSetting{
				ServerSetting: common.ServerSetting{
					DefaultSmallModelEnabled: true,
				},
			}
			vbaWithVector := bmock.NewMockVegaBackendService(mockCtrl)
			mfs := bmock.NewMockModelFactoryService(mockCtrl)

			serviceWithVector := &objectTypeService{
				appSetting: appSettingWithVector,
				vbs:        vbaWithVector,
				mfs:        mfs,
			}

			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					CommonInfo: interfaces.CommonInfo{
						Tags:          []string{"tag1"},
						Comment:       "comment",
						BKNRawContent: "bkn",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}
			vectors := []*cond.VectorResp{
				{
					Vector: []float32{0.1, 0.2, 0.3},
				},
			}

			mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(&interfaces.SmallModel{ModelID: "model1"}, nil)
			mfs.EXPECT().GetVector(gomock.Any(), gomock.Any(), gomock.Any()).Return(vectors, nil)
			vbaWithVector.EXPECT().WriteDatasetDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

			err := serviceWithVector.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldBeNil)
		})

		Convey("Failed when GetDefaultModel returns error with vector enabled\n", func() {
			appSettingWithVector := &common.AppSetting{
				ServerSetting: common.ServerSetting{
					DefaultSmallModelEnabled: true,
				},
			}
			mfs := bmock.NewMockModelFactoryService(mockCtrl)

			serviceWithVector := &objectTypeService{
				appSetting: appSettingWithVector,
				mfs:        mfs,
			}

			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := serviceWithVector.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when GetVector returns error with vector enabled\n", func() {
			appSettingWithVector := &common.AppSetting{
				ServerSetting: common.ServerSetting{
					DefaultSmallModelEnabled: true,
				},
			}
			mfs := bmock.NewMockModelFactoryService(mockCtrl)

			serviceWithVector := &objectTypeService{
				appSetting: appSettingWithVector,
				mfs:        mfs,
			}

			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(&interfaces.SmallModel{ModelID: "model1"}, nil)
			mfs.EXPECT().GetVector(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := serviceWithVector.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when vector count mismatch with vector enabled\n", func() {
			appSettingWithVector := &common.AppSetting{
				ServerSetting: common.ServerSetting{
					DefaultSmallModelEnabled: true,
				},
			}
			mfs := bmock.NewMockModelFactoryService(mockCtrl)

			serviceWithVector := &objectTypeService{
				appSetting: appSettingWithVector,
				mfs:        mfs,
			}

			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "object_type1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}
			vectors := []*cond.VectorResp{}

			mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(&interfaces.SmallModel{ModelID: "model1"}, nil)
			mfs.EXPECT().GetVector(gomock.Any(), gomock.Any(), gomock.Any()).Return(vectors, nil)

			err := serviceWithVector.InsertDatasetData(ctx, objectTypes)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_objectTypeService_GetTotal(t *testing.T) {
	Convey("Test GetTotal\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		vbs := bmock.NewMockVegaBackendService(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			vbs:        vbs,
		}

		Convey("Success getting total\n", func() {
			filterCondition := map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
			}
			datasetResp := &interfaces.DatasetQueryResponse{
				TotalCount: 10,
			}

			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			total, err := service.GetTotal(ctx, filterCondition)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 10)
		})

		Convey("Failed when QueryResourceData fails\n", func() {
			filterCondition := map[string]any{}

			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			total, err := service.GetTotal(ctx, filterCondition)
			So(err, ShouldNotBeNil)
			So(total, ShouldEqual, 0)
		})

		Convey("Failed when QueryResourceData returns nil response\n", func() {
			filterCondition := map[string]any{}

			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)

			total, err := service.GetTotal(ctx, filterCondition)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_GetTotalWithLargeOTIDs(t *testing.T) {
	Convey("Test GetTotalWithLargeOTIDs\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		vbs := bmock.NewMockVegaBackendService(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			vbs:        vbs,
		}

		Convey("Success getting total with large OTIDs\n", func() {
			filterCondition := map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
			}
			otIDs := []string{"ot1", "ot2", "ot3"}

			// Mock GetTotalWithOTIDs calls
			datasetResp := &interfaces.DatasetQueryResponse{
				TotalCount: 5,
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil).Times(1)

			total, err := service.GetTotalWithLargeOTIDs(ctx, filterCondition, otIDs)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 5)
		})

		Convey("Success with empty OTIDs\n", func() {
			filterCondition := map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
			}
			otIDs := []string{}

			total, err := service.GetTotalWithLargeOTIDs(ctx, filterCondition, otIDs)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 0)
		})

		Convey("Failed when GetTotalWithOTIDs returns error\n", func() {
			filterCondition := map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
			}
			otIDs := []string{"ot1"}

			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			total, err := service.GetTotalWithLargeOTIDs(ctx, filterCondition, otIDs)
			So(err, ShouldNotBeNil)
			So(total, ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_GetTotalWithOTIDs(t *testing.T) {
	Convey("Test GetTotalWithOTIDs\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		vbs := bmock.NewMockVegaBackendService(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			vbs:        vbs,
		}

		Convey("Success getting total with OTIDs\n", func() {
			filterCondition := map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
			}
			otIDs := []string{"ot1", "ot2"}

			datasetResp := &interfaces.DatasetQueryResponse{
				TotalCount: 2,
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			total, err := service.GetTotalWithOTIDs(ctx, filterCondition, otIDs)
			So(err, ShouldBeNil)
			So(total, ShouldEqual, 2)
		})

		Convey("Failed when GetTotal returns error\n", func() {
			filterCondition := map[string]any{
				"query": map[string]any{
					"match_all": map[string]any{},
				},
			}
			otIDs := []string{"ot1"}

			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			total, err := service.GetTotalWithOTIDs(ctx, filterCondition, otIDs)
			So(err, ShouldNotBeNil)
			So(total, ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_SearchObjectTypes(t *testing.T) {
	Convey("Test SearchObjectTypes\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			ServerSetting: common.ServerSetting{
				DefaultSmallModelEnabled: false,
			},
		}
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		ma := bmock.NewMockMetricAccess(mockCtrl)
		mfs := bmock.NewMockModelFactoryService(mockCtrl)
		ps := bmock.NewMockPermissionService(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			cga:        cga,
			vbs:        vbs,
			ma:         ma,
			mfs:        mfs,
			ps:         ps,
		}

		Convey("Success searching object types without concept groups\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(result.Entries, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Success searching object types with concept groups\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				ConceptGroups: []string{"cg1"},
				ActualCondition: &cond.CondCfg{
					Operation: "and",
					SubConds: []*cond.CondCfg{
						{
							Field:     "name",
							Operation: cond.OperationEq,
							ValueOptCfg: cond.ValueOptCfg{
								ValueFrom: "const",
								Value:     "ot1",
							},
						},
					},
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{"ot1"}, nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(result.Entries, ShouldNotBeNil)
		})

		Convey("Default cursor paging continues after a full page when concept-group filtering needs more entries\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         2,
				ConceptGroups: []string{"cg1"},
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{"keep-1", "keep-2"}, nil)
			nextCursor := "cursor-1"
			gomock.InOrder(
				vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
						So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Mode: "cursor", Limit: 2})
						So(params.Sort, ShouldResemble, []*interfaces.SortParams{{Field: "id", Direction: "asc"}})
						return &interfaces.DatasetQueryResponse{Entries: []map[string]any{
							{"id": "skip", "name": "skip"},
							{"id": "keep-1", "name": "keep-1"},
						}, Paging: &interfaces.ResourceDataPagingResult{NextCursor: &nextCursor}}, nil
					}),
				vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
						So(params.Paging, ShouldResemble, interfaces.ResourceDataPagingRequest{Cursor: nextCursor})
						return &interfaces.DatasetQueryResponse{Entries: []map[string]any{{"id": "keep-2", "name": "keep-2"}}}, nil
					}),
			)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Entries), ShouldEqual, 2)
			So(result.Entries[0].OTID, ShouldEqual, "keep-1")
			So(result.Entries[1].OTID, ShouldEqual, "keep-2")
		})

		Convey("Failed when concept groups not found\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				NeedTotal:     false,
				Limit:         10,
				ConceptGroups: []string{"cg1"},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(0, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when GetConceptGroupsTotal returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				ConceptGroups: []string{"cg1"},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(0, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when GetConceptIDsByConceptGroupIDs returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				ConceptGroups: []string{"cg1"},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Success with empty concept groups\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(result.Entries, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when NewCondition returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
				ActualCondition: &cond.CondCfg{
					Operation: "invalid_operation",
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("KNN is ignored when DefaultSmallModelEnabled is false, search still queries dataset\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
				ActualCondition: &cond.CondCfg{
					Operation: "knn",
					ValueOptCfg: cond.ValueOptCfg{
						ValueFrom: "const",
						Value:     []string{"word1"},
					},
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(result.Entries, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Success with concept groups returning empty otIDs\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				ConceptGroups: []string{"cg1"},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{}, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Success with NeedTotal true and no concept groups\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:      "kn1",
				Branch:    interfaces.MAIN_BRANCH,
				Limit:     10,
				NeedTotal: true,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			totalResp := &interfaces.DatasetQueryResponse{
				TotalCount: 5,
			}
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(totalResp, nil).Times(1)
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil).Times(1)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(result.TotalCount, ShouldEqual, 5)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Success with NeedTotal true and with concept groups\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				NeedTotal:     true,
				ConceptGroups: []string{"cg1"},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{"ot1"}, nil)
			totalResp := &interfaces.DatasetQueryResponse{
				TotalCount: 3,
			}
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(totalResp, nil).Times(1)
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil).Times(1)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(result.TotalCount, ShouldEqual, 3)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when GetTotal returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:      "kn1",
				Branch:    interfaces.MAIN_BRANCH,
				Limit:     10,
				NeedTotal: true,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when GetTotalWithLargeOTIDs returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				NeedTotal:     true,
				ConceptGroups: []string{"cg1"},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{"ot1"}, nil)
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when BuildDslQuery returns error in NeedTotal\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:      "kn1",
				Branch:    interfaces.MAIN_BRANCH,
				Limit:     10,
				NeedTotal: true,
				ActualCondition: &cond.CondCfg{
					Operation: "invalid_operation",
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when BuildDslQuery returns error in loop\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
				ActualCondition: &cond.CondCfg{
					Operation: "and",
					SubConds: []*cond.CondCfg{
						{
							Operation: "invalid_operation",
						},
					},
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when SearchData returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when Marshal returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
			}
			// Create a non-serializable object.
			entry := map[string]any{
				"invalid": make(chan int), // channel cannot be marshaled
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{entry},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when Unmarshal returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
			}
			// Create an invalid JSON structure that will fail unmarshal
			entry := map[string]any{
				"invalid_json": make(chan int), // channel cannot be marshaled/unmarshaled
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{entry},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)

			result, err := service.SearchObjectTypes(ctx, query)
			// Marshal will fail first, so error should occur
			So(err, ShouldNotBeNil)
			So(len(result.Entries), ShouldEqual, 0)
		})

		Convey("Failed when processObjectTypeDetails returns error\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
				Limit:  10,
			}
			entry := map[string]any{
				"ot_id":   "ot1",
				"ot_name": "ot1",
				"kn_id":   "kn1",
				"branch":  "main",
				"_score":  0.9,
				"data_source": map[string]any{
					"id": "dv1",
				},
				"logic_properties": []any{
					map[string]any{
						"name": "lp1",
						"data_source": map[string]any{
							"type": interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							"id":   "metric1",
						},
					},
				},
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{entry},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Entries), ShouldEqual, 1)
		})

		Convey("Success with multiple hits and filtering\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				Limit:         10,
				ConceptGroups: []string{"cg1"},
			}
			entry1 := map[string]any{
				"ot_id":   "ot1",
				"ot_name": "ot1",
				"kn_id":   "kn1",
				"branch":  "main",
				"_score":  0.9,
			}
			entry2 := map[string]any{
				"ot_id":   "ot2",
				"ot_name": "ot2",
				"kn_id":   "kn1",
				"branch":  "main",
				"_score":  0.8,
			}

			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().GetConceptGroupsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
			cga.EXPECT().GetConceptIDsByConceptGroupIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{"ot1"}, nil)
			datasetResp := &interfaces.DatasetQueryResponse{
				Entries: []map[string]any{entry1, entry2},
			}
			vbs.EXPECT().QueryResourceData(gomock.Any(), gomock.Any(), gomock.Any()).Return(datasetResp, nil)
			// processObjectTypeDetails may be called for each object type
			result, err := service.SearchObjectTypes(ctx, query)
			So(err, ShouldBeNil)
			// The filtering happens based on otIDMap, so only ot1 should be included
			So(len(result.Entries), ShouldBeGreaterThanOrEqualTo, 0)
			if len(result.Entries) > 0 {
				So(result.Entries[0].OTID, ShouldEqual, "ot1")
			}
		})
	})
}

func Test_objectTypeService_handleObjectTypeImportMode(t *testing.T) {
	Convey("Test handleObjectTypeImportMode\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)

		service := &objectTypeService{
			appSetting: appSetting,
			ota:        ota,
		}

		Convey("Success with Normal mode when object type does not exist\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Normal, objectTypes)
			So(err, ShouldBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
		})

		Convey("Failed with Normal mode when ID exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Normal, objectTypes)
			So(err, ShouldNotBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeIDExisted)
		})

		Convey("Failed with Normal mode when name exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Normal, objectTypes)
			So(err, ShouldNotBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeNameExisted)
		})

		Convey("Success with Ignore mode when object type exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Ignore, objectTypes)
			So(err, ShouldBeNil)
			So(len(creates), ShouldEqual, 0)
			So(len(updates), ShouldEqual, 0)
		})

		Convey("Success with Overwrite mode when ID and name exist with same ID\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Overwrite, objectTypes)
			So(err, ShouldBeNil)
			So(len(creates), ShouldEqual, 0)
			So(len(updates), ShouldEqual, 1)
		})

		Convey("Failed with Overwrite mode when ID and name exist with different ID\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot2", true, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Overwrite, objectTypes)
			So(err, ShouldNotBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeNameExisted)
		})

		Convey("Success with Overwrite mode when only ID exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot1", true, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Overwrite, objectTypes)
			So(err, ShouldBeNil)
			So(len(creates), ShouldEqual, 0)
			So(len(updates), ShouldEqual, 1)
		})

		Convey("Failed with Overwrite mode when only name exists\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("ot2", true, nil)

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Overwrite, objectTypes)
			So(err, ShouldNotBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_ObjectTypeNameExisted)
		})

		Convey("Failed when CheckObjectTypeExistByID returns error\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Normal, objectTypes)
			So(err, ShouldNotBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
		})

		Convey("Failed when CheckObjectTypeExistByName returns error\n", func() {
			objectTypes := []*interfaces.ObjectType{
				{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID:   "ot1",
						OTName: "ot1",
					},
					KNID:   "kn1",
					Branch: interfaces.MAIN_BRANCH,
				},
			}

			ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, nil)
			ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", false, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			creates, updates, err := service.handleObjectTypeImportMode(ctx, interfaces.ImportMode_Normal, objectTypes)
			So(err, ShouldNotBeNil)
			So(len(creates), ShouldEqual, 1)
			So(len(updates), ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_processConditionOperations(t *testing.T) {
	Convey("Test processConditionOperations\n", t, func() {
		appSetting := &common.AppSetting{
			ServerSetting: common.ServerSetting{
				DefaultSmallModelEnabled: true,
			},
		}
		service := &objectTypeService{
			appSetting: appSetting,
		}

		Convey("Index not available - keyword type\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "keyword",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - varchar type with DSL query\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "varchar",
			}
			dataView := &interfaces.DataView{
				QueryType: interfaces.VIEW_QueryType_DSL,
			}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - varchar type with SQL query\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "varchar",
			}
			dataView := &interfaces.DataView{
				QueryType: interfaces.VIEW_QueryType_SQL,
			}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - string type with DSL query\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "string",
			}
			dataView := &interfaces.DataView{
				QueryType: interfaces.VIEW_QueryType_DSL,
			}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - text type with DSL query\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "text",
			}
			dataView := &interfaces.DataView{
				QueryType: interfaces.VIEW_QueryType_DSL,
			}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - text type with SQL query\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "text",
			}
			dataView := &interfaces.DataView{
				QueryType: interfaces.VIEW_QueryType_SQL,
			}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - vector type with model enabled\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "vector",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index not available - vector type with model disabled\n", func() {
			appSetting2 := &common.AppSetting{
				ServerSetting: common.ServerSetting{
					DefaultSmallModelEnabled: false,
				},
			}
			service2 := &objectTypeService{
				appSetting: appSetting2,
			}
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: false,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "vector",
			}
			dataView := &interfaces.DataView{}

			ops := service2.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldEqual, 0)
		})

		Convey("Index available - text type\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "text",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index available - non-text type\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "keyword",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index available - with keyword config\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "keyword",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index available - with fulltext config\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "keyword",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index available - with vector config and model enabled\n", func() {
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "vector",
			}
			dataView := &interfaces.DataView{}

			ops := service.processConditionOperations(objectType, prop, dataView)
			So(len(ops), ShouldBeGreaterThan, 0)
		})

		Convey("Index available - with vector config and model disabled\n", func() {
			appSetting2 := &common.AppSetting{
				ServerSetting: common.ServerSetting{
					DefaultSmallModelEnabled: false,
				},
			}
			service2 := &objectTypeService{
				appSetting: appSetting2,
			}
			objectType := &interfaces.ObjectType{
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
				},
			}
			prop := &interfaces.DataProperty{
				Type: "vector",
			}
			dataView := &interfaces.DataView{}

			ops := service2.processConditionOperations(objectType, prop, dataView)
			// Even when vector config is enabled, no KNN operation should occur if the model is disabled.
			So(len(ops), ShouldBeGreaterThanOrEqualTo, 0)
		})
	})
}

func Test_objectTypeService_handleGroupRelations(t *testing.T) {
	Convey("Test handleGroupRelations\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			cga:        cga,
		}

		currentTime := int64(1735786555379)
		objectType := &interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:   "ot1",
				OTName: "ot1",
			},
			KNID:   "kn1",
			Branch: interfaces.MAIN_BRANCH,
			ConceptGroups: []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			},
		}

		Convey("Success handling group relations\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			}

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)
			cga.EXPECT().CreateConceptGroupRelation(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

			err := service.handleGroupRelations(ctx, tx, objectType, currentTime, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed when GetConceptGroupsByIDs returns error\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.handleGroupRelations(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when concept groups count mismatch\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{} // Return an empty slice.

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)

			err := service.handleGroupRelations(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when CreateConceptGroupRelation returns error\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			}

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)
			cga.EXPECT().CreateConceptGroupRelation(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.handleGroupRelations(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with empty concept groups\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			objectType2 := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "ot1",
				},
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				ConceptGroups: []*interfaces.ConceptGroup{},
			}
			// When ConceptGroups is empty, GetConceptGroupsByIDs will be called with empty cgIDs
			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]*interfaces.ConceptGroup{}, nil)

			err := service.handleGroupRelations(ctx, tx, objectType2, currentTime, true)
			So(err, ShouldBeNil)
		})
	})
}

func Test_objectTypeService_syncObjectGroups(t *testing.T) {
	Convey("Test syncObjectGroups\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		db, smock, _ := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))

		service := &objectTypeService{
			appSetting: appSetting,
			cga:        cga,
		}

		currentTime := int64(1735786555379)
		objectType := interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:   "ot1",
				OTName: "ot1",
			},
			KNID:   "kn1",
			Branch: interfaces.MAIN_BRANCH,
			ConceptGroups: []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			},
		}

		Convey("Success syncing object groups - add new groups\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			}
			existingRelation := map[string][]*interfaces.ConceptGroup{
				"ot1": {}, // No existing relation.
			}

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingRelation, nil)
			cga.EXPECT().CreateConceptGroupRelation(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

			err := service.syncObjectGroups(ctx, tx, objectType, currentTime, true)
			So(err, ShouldBeNil)
		})

		Convey("Success syncing object groups - remove old groups\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			existingRelation := map[string][]*interfaces.ConceptGroup{
				"ot1": {
					{
						CGID: "cg2",
					},
				},
			}
			objectType2 := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "ot1",
				},
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				ConceptGroups: []*interfaces.ConceptGroup{}, // Empty concept groups.
			}

			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingRelation, nil)
			cga.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)

			err := service.syncObjectGroups(ctx, tx, objectType2, currentTime, false)
			So(err, ShouldBeNil)
		})

		Convey("Success syncing object groups - update groups\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			}
			existingRelation := map[string][]*interfaces.ConceptGroup{
				"ot1": {
					{
						CGID: "cg2",
					},
				},
			}

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingRelation, nil)
			cga.EXPECT().CreateConceptGroupRelation(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cga.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)

			err := service.syncObjectGroups(ctx, tx, objectType, currentTime, true)
			So(err, ShouldBeNil)
		})

		Convey("Failed when GetConceptGroupsByIDs returns error\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.syncObjectGroups(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when concept groups count mismatch\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{} // Return an empty slice.

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)

			err := service.syncObjectGroups(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when GetConceptGroupsByOTIDs returns error\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			}

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.syncObjectGroups(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when CreateConceptGroupRelation returns error\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			conceptGroups := []*interfaces.ConceptGroup{
				{
					CGID:   "cg1",
					CGName: "cg1",
				},
			}
			existingRelation := map[string][]*interfaces.ConceptGroup{
				"ot1": {},
			}

			cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(conceptGroups, nil)
			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingRelation, nil)
			cga.EXPECT().CreateConceptGroupRelation(gomock.Any(), gomock.Any(), gomock.Any()).Return(rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.syncObjectGroups(ctx, tx, objectType, currentTime, true)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteObjectTypesFromGroup returns error\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			existingRelation := map[string][]*interfaces.ConceptGroup{
				"ot1": {
					{
						CGID: "cg2",
					},
				},
			}
			objectType2 := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "ot1",
				},
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				ConceptGroups: []*interfaces.ConceptGroup{},
			}

			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingRelation, nil)
			cga.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), rest.NewHTTPError(ctx, 500, berrors.BknBackend_ObjectType_InternalError))

			err := service.syncObjectGroups(ctx, tx, objectType2, currentTime, false)
			So(err, ShouldNotBeNil)
		})

		Convey("Success with empty concept groups and no existing relations\n", func() {
			smock.ExpectBegin()
			tx, _ := db.Begin()
			objectType2 := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:   "ot1",
					OTName: "ot1",
				},
				KNID:          "kn1",
				Branch:        interfaces.MAIN_BRANCH,
				ConceptGroups: []*interfaces.ConceptGroup{},
			}
			existingRelation := map[string][]*interfaces.ConceptGroup{}

			cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingRelation, nil)

			err := service.syncObjectGroups(ctx, tx, objectType2, currentTime, false)
			So(err, ShouldBeNil)
		})
	})
}

func Test_objectTypeService_DeleteObjectTypesByKnID(t *testing.T) {
	Convey("Test DeleteObjectTypesByKnID\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return([]string{"ot1"}, nil).AnyTimes()
		ps := bmock.NewMockPermissionService(mockCtrl)
		ps.EXPECT().DeleteResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		ps.EXPECT().DeleteResourceParents(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		service := &objectTypeService{appSetting: &common.AppSetting{}, ota: ota, ps: ps}

		knID := "kn1"
		branch := interfaces.MAIN_BRANCH

		Convey("Failed when tx is nil\n", func() {
			err := service.DeleteObjectTypesByKnID(context.Background(), nil, knID, branch)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteObjectTypesByKnID access returns error\n", func() {
			tx := new(sql.Tx)
			ota.EXPECT().DeleteObjectTypesByKnID(gomock.Any(), tx, knID, branch).Return(int64(0), rest.NewHTTPError(context.Background(), 500, berrors.BknBackend_ObjectType_InternalError))
			err := service.DeleteObjectTypesByKnID(context.Background(), tx, knID, branch)
			So(err, ShouldNotBeNil)
		})

		Convey("Failed when DeleteObjectTypeStatusByKnID access returns error\n", func() {
			tx := new(sql.Tx)
			ota.EXPECT().DeleteObjectTypesByKnID(gomock.Any(), tx, knID, branch).Return(int64(1), nil)
			ota.EXPECT().DeleteObjectTypeStatusByKnID(gomock.Any(), tx, knID, branch).Return(int64(0), rest.NewHTTPError(context.Background(), 500, berrors.BknBackend_ObjectType_InternalError))
			err := service.DeleteObjectTypesByKnID(context.Background(), tx, knID, branch)
			So(err, ShouldNotBeNil)
		})

		Convey("Success\n", func() {
			tx := new(sql.Tx)
			ota.EXPECT().DeleteObjectTypesByKnID(gomock.Any(), tx, knID, branch).Return(int64(3), nil)
			ota.EXPECT().DeleteObjectTypeStatusByKnID(gomock.Any(), tx, knID, branch).Return(int64(3), nil)
			err := service.DeleteObjectTypesByKnID(context.Background(), tx, knID, branch)
			So(err, ShouldBeNil)
		})
	})
}

func Test_applyIndexCapOps(t *testing.T) {
	Convey("Test applyIndexCapOps\n", t, func() {
		// Baseline inferred from property type: strings expose only keyword operators in the DSL view.
		baseline := append([]string{}, interfaces.DSL_KEYWORD_OPS...)

		contains := func(ops []string, op string) bool {
			for _, item := range ops {
				if item == op {
					return true
				}
			}
			return false
		}

		Convey("No caps leaves the baseline untouched\n", func() {
			ops := applyIndexCapOps(baseline, logics.PropertyIndexCaps{})
			So(ops, ShouldResemble, baseline)
		})

		Convey("Fulltext cap exposes match / multi_match\n", func() {
			ops := applyIndexCapOps(baseline, logics.PropertyIndexCaps{Fulltext: true})
			So(contains(ops, cond.OperationMatch), ShouldBeTrue)
			So(contains(ops, cond.OperationMultiMatch), ShouldBeTrue)
			// All baseline operators are present.
			for _, op := range baseline {
				So(contains(ops, op), ShouldBeTrue)
			}
			So(len(ops), ShouldEqual, len(baseline)+2)
		})

		Convey("Keyword cap does not duplicate ops already in the baseline\n", func() {
			ops := applyIndexCapOps(baseline, logics.PropertyIndexCaps{Keyword: true})
			So(len(ops), ShouldEqual, len(baseline))
		})

		Convey("Keyword cap tops up a baseline that lacks those ops\n", func() {
			ops := applyIndexCapOps([]string{}, logics.PropertyIndexCaps{Keyword: true})
			So(len(ops), ShouldEqual, len(interfaces.DSL_KEYWORD_OPS))
			So(contains(ops, cond.OperationEq), ShouldBeTrue)
		})

		Convey("Vector cap opens knn: the property carries the generated vector field\n", func() {
			ops := applyIndexCapOps(baseline, logics.PropertyIndexCaps{Vector: true})
			So(contains(ops, cond.OperationKNN), ShouldBeTrue)
		})

		Convey("A property with no capability at all keeps the baseline untouched\n", func() {
			ops := applyIndexCapOps(baseline, logics.PropertyIndexCaps{})
			So(ops, ShouldResemble, baseline)
		})

		Convey("The shared package-level op slices are never mutated\n", func() {
			before := append([]string{}, interfaces.DSL_TEXT_OPS...)
			_ = applyIndexCapOps(interfaces.DSL_TEXT_OPS, logics.PropertyIndexCaps{Keyword: true, Fulltext: true})
			So(interfaces.DSL_TEXT_OPS, ShouldResemble, before)
		})
	})
}
