// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package value_type

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

type fixture struct {
	svc   *valueTypeService
	vta   *bmock.MockValueTypeAccess
	ota   *bmock.MockObjectTypeAccess
	ps    *bmock.MockPermissionService
	smock sqlmock.Sqlmock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{
		vta:   bmock.NewMockValueTypeAccess(ctrl),
		ota:   bmock.NewMockObjectTypeAccess(ctrl),
		ps:    bmock.NewMockPermissionService(ctrl),
		smock: smock,
	}
	f.svc = &valueTypeService{db: db, vta: f.vta, ota: f.ota, ps: f.ps}
	return f
}

func (f *fixture) allow(operation string) {
	f.ps.EXPECT().CheckPermission(gomock.Any(),
		interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: "kn1"}, []string{operation}).Return(nil)
}

// referencedBy makes the network hold one object type whose property amount (Type propertyType)
// names amount_cny.
func (f *fixture) referencedBy(propertyType string) {
	f.ota.EXPECT().GetAllObjectTypesByKnID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(map[string]*interfaces.ObjectType{
		"order": {ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单",
			DataProperties: []*interfaces.DataProperty{
				{Name: "id", Type: "string"},
				{Name: "amount", Type: propertyType, Constraints: &interfaces.PropertyConstraints{ValueType: "amount_cny"}},
			}}},
		"refund": {ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "refund", OTName: "退款"}},
	}, nil)
}

func (f *fixture) unreferenced() {
	f.ota.EXPECT().GetAllObjectTypesByKnID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(map[string]*interfaces.ObjectType{}, nil)
}

func amountCNY() *interfaces.ValueType {
	min := 0
	return &interfaces.ValueType{
		VTID: "amount_cny", VTName: "人民币金额", KNID: "kn1", Branch: interfaces.MAIN_BRANCH, BaseType: "decimal",
		ValueTypeDefinition: interfaces.ValueTypeDefinition{Range: &interfaces.ValueRange{Min: float64(min)}, Unit: "元"},
	}
}

func lisStatus() *interfaces.ValueType {
	return &interfaces.ValueType{
		VTID: "lis_status", VTName: "检验申请状态", KNID: "kn1", Branch: interfaces.MAIN_BRANCH, BaseType: "string",
		ValueTypeDefinition: interfaces.ValueTypeDefinition{Enum: []*interfaces.ValueTypeEnumItem{
			{Value: "A", Display: "已申请"}, {Value: "C", Display: "已撤销", Deprecated: true},
		}},
	}
}

func httpError(t *testing.T, err error) *rest.HTTPError {
	t.Helper()
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr), "%v", err)
	return httpErr
}

func TestCreateValueTypes(t *testing.T) {
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u1", Type: "user"})

	t.Run("creates and derives the stored file", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), "kn1", interfaces.MAIN_BRANCH,
			[]string{"amount_cny", "lis_status"}, []string{"人民币金额", "检验申请状态"}).Return(nil, nil)
		f.smock.ExpectBegin()
		var created []*interfaces.ValueType
		f.vta.EXPECT().CreateValueTypes(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, vts []*interfaces.ValueType) error {
				created = vts
				return nil
			})
		f.smock.ExpectCommit()

		ids, err := f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{amountCNY(), lisStatus()}, interfaces.ImportMode_Normal)
		require.NoError(t, err)
		assert.Equal(t, []string{"amount_cny", "lis_status"}, ids)
		require.Len(t, created, 2)
		assert.Contains(t, created[0].BKNRawContent, "## ValueType: 人民币金额")
		assert.Contains(t, created[1].BKNRawContent, "deprecated: true")
		assert.Equal(t, "u1", created[0].Creator.ID)
		assert.Equal(t, interfaces.MODULE_TYPE_VALUE_TYPE, created[0].ModuleType)
		assert.NoError(t, f.smock.ExpectationsWereMet())
	})

	t.Run("needs modify on the network", func(t *testing.T) {
		f := newFixture(t)
		f.ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), []string{interfaces.OPERATION_TYPE_MODIFY}).
			Return(rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_ValueType_InternalError))
		_, err := f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{amountCNY()}, interfaces.ImportMode_Normal)
		assert.Equal(t, http.StatusForbidden, httpError(t, err).HTTPCode)
	})

	t.Run("refuses value rules the specification refuses", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		vt := lisStatus()
		vt.Pattern = "^[A-Z]$"
		_, err := f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{vt}, interfaces.ImportMode_Normal)
		httpErr := httpError(t, err)
		assert.Equal(t, berrors.BknBackend_ValueType_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "range, length and pattern do not go with it")

		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		vt = lisStatus()
		vt.Enum = append(vt.Enum, nil)
		_, err = f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{vt}, interfaces.ImportMode_Normal)
		assert.Contains(t, httpError(t, err).BaseError.ErrorDetails, "an enum item is null")

		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		vt = amountCNY()
		vt.BaseType = "money"
		_, err = f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{vt}, interfaces.ImportMode_Normal)
		assert.Contains(t, httpError(t, err).BaseError.ErrorDetails, "base_type must be one of")
	})

	t.Run("import modes", func(t *testing.T) {
		existing := []*interfaces.ValueType{{VTID: "amount_cny", VTName: "人民币金额"}}

		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(existing, nil)
		_, err := f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{amountCNY()}, interfaces.ImportMode_Normal)
		assert.Equal(t, berrors.BknBackend_ValueType_ValueTypeIDExisted, httpError(t, err).BaseError.ErrorCode)

		f = newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(existing, nil)
		f.smock.ExpectBegin()
		f.smock.ExpectCommit()
		ids, err := f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{amountCNY()}, interfaces.ImportMode_Ignore)
		require.NoError(t, err)
		assert.Empty(t, ids)

		f = newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(existing, nil)
		f.smock.ExpectBegin()
		f.unreferenced()
		f.vta.EXPECT().UpdateValueType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.smock.ExpectCommit()
		ids, err = f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{amountCNY()}, interfaces.ImportMode_Overwrite)
		require.NoError(t, err)
		assert.Equal(t, []string{"amount_cny"}, ids)

		f = newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*interfaces.ValueType{{VTID: "other", VTName: "人民币金额"}}, nil)
		_, err = f.svc.CreateValueTypes(ctx, nil, []*interfaces.ValueType{amountCNY()}, interfaces.ImportMode_Overwrite)
		assert.Equal(t, berrors.BknBackend_ValueType_ValueTypeNameExisted, httpError(t, err).BaseError.ErrorCode,
			"overwrite does not take a name another value type holds")
	})
}

func TestUpdateValueType(t *testing.T) {
	ctx := context.Background()

	t.Run("rechecks the properties that reference it", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.smock.ExpectBegin()
		f.vta.EXPECT().GetValueTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).
			Return([]*interfaces.ValueType{amountCNY()}, nil)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, nil, []string{"人民币金额"}).
			Return([]*interfaces.ValueType{{VTID: "amount_cny", VTName: "人民币金额"}}, nil)
		f.referencedBy("decimal")
		f.smock.ExpectRollback()

		vt := amountCNY()
		vt.BaseType = "integer"
		vt.Range = nil
		err := f.svc.UpdateValueType(ctx, nil, vt)
		httpErr := httpError(t, err)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, berrors.BknBackend_ValueType_Referenced, httpErr.BaseError.ErrorCode)
		details := httpErr.BaseError.ErrorDetails.(map[string]any)
		assert.Equal(t, []interfaces.ValueTypeReference{
			{ObjectTypeID: "order", ObjectTypeName: "订单", Property: "amount", PropertyType: "decimal"},
		}, details["references"])
		assert.NoError(t, f.smock.ExpectationsWereMet())
	})

	t.Run("keeps a base_type its references fit", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.smock.ExpectBegin()
		f.vta.EXPECT().GetValueTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*interfaces.ValueType{amountCNY()}, nil)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
		f.referencedBy(" Decimal ")
		f.vta.EXPECT().UpdateValueType(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.smock.ExpectCommit()

		vt := amountCNY()
		vt.Unit = "万元"
		require.NoError(t, f.svc.UpdateValueType(ctx, nil, vt))
		assert.Contains(t, vt.BKNRawContent, "unit: 万元")
	})

	t.Run("refuses a name another value type holds", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.smock.ExpectBegin()
		f.vta.EXPECT().GetValueTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*interfaces.ValueType{amountCNY()}, nil)
		f.vta.EXPECT().GetValueTypeIdentitiesByIDsOrNames(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*interfaces.ValueType{{VTID: "other", VTName: "人民币金额"}}, nil)
		f.smock.ExpectRollback()
		err := f.svc.UpdateValueType(ctx, nil, amountCNY())
		assert.Equal(t, berrors.BknBackend_ValueType_ValueTypeNameExisted, httpError(t, err).BaseError.ErrorCode)
	})

	t.Run("not found", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.smock.ExpectBegin()
		f.vta.EXPECT().GetValueTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
		f.smock.ExpectRollback()
		err := f.svc.UpdateValueType(ctx, nil, amountCNY())
		assert.Equal(t, http.StatusNotFound, httpError(t, err).HTTPCode)
	})
}

func TestDeleteValueType(t *testing.T) {
	ctx := context.Background()

	t.Run("refuses while a property references it", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.smock.ExpectBegin()
		f.vta.EXPECT().GetValueTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*interfaces.ValueType{amountCNY()}, nil)
		f.referencedBy("decimal")
		f.smock.ExpectRollback()
		err := f.svc.DeleteValueType(ctx, nil, "kn1", interfaces.MAIN_BRANCH, "amount_cny")
		httpErr := httpError(t, err)
		assert.Equal(t, berrors.BknBackend_ValueType_Referenced, httpErr.BaseError.ErrorCode)
		assert.Len(t, httpErr.BaseError.ErrorDetails.(map[string]any)["references"], 1)
		assert.NoError(t, f.smock.ExpectationsWereMet())
	})

	t.Run("deletes an unreferenced value type", func(t *testing.T) {
		f := newFixture(t)
		f.allow(interfaces.OPERATION_TYPE_MODIFY)
		f.smock.ExpectBegin()
		f.vta.EXPECT().GetValueTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*interfaces.ValueType{amountCNY()}, nil)
		f.unreferenced()
		f.vta.EXPECT().DeleteValueTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).Return(int64(1), nil)
		f.smock.ExpectCommit()
		require.NoError(t, f.svc.DeleteValueType(ctx, nil, "kn1", interfaces.MAIN_BRANCH, "amount_cny"))
		assert.NoError(t, f.smock.ExpectationsWereMet())
	})
}

func TestListValueTypes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(nil).Times(2)
	all := []*interfaces.ValueType{amountCNY(), lisStatus(), {VTID: "rating"}}
	f.vta.EXPECT().ListValueTypes(gomock.Any(), gomock.Any()).Return(all, nil).Times(2)

	page, total, err := f.svc.ListValueTypes(ctx, interfaces.ValueTypesQueryParams{KNID: "kn1",
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 1, Limit: 1}})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Equal(t, []*interfaces.ValueType{all[1]}, page)

	page, _, err = f.svc.ListValueTypes(ctx, interfaces.ValueTypesQueryParams{KNID: "kn1",
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Offset: 5, Limit: -1}})
	require.NoError(t, err)
	assert.Empty(t, page)
}
