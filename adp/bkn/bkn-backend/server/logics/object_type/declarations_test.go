// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// declaredObjectType is a stored object type with property declarations.
func declaredObjectType() *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "order", OTName: "订单", PrimaryKeys: []string{"id"}, DisplayKey: "id",
			DataProperties: []*interfaces.DataProperty{
				{Name: "id", Type: "string", Required: true},
				{Name: "placed_at", Type: "datetime", Semantic: "event_time", Required: true},
				{Name: "amount", Type: "decimal", Constraints: &interfaces.PropertyConstraints{Range: &interfaces.ValueRange{Min: float64(0)}, Unit: "元"}},
			},
		},
		KNID: "kn1", Branch: interfaces.MAIN_BRANCH,
	}
}

func errorCode(t *testing.T, err error) string {
	t.Helper()
	var httpErr *httperrors.HTTPError
	require.True(t, errors.As(err, &httpErr), "%v", err)
	return httpErr.ErrorCode
}

func decodeProperty(t *testing.T, text string) *interfaces.DataProperty {
	t.Helper()
	var p interfaces.DataProperty
	require.NoError(t, json.Unmarshal([]byte(text), &p))
	return &p
}

// A save runs the checks a 3.0 import runs.
func TestValidateDeclarations(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, validateDeclarations(ctx, declaredObjectType()))

	ot := declaredObjectType()
	ot.DataProperties[1].Semantic = "end_time"
	err := validateDeclarations(ctx, ot)
	assert.Equal(t, berrors.BknBackend_ObjectType_InvalidParameter_Declarations, errorCode(t, err))
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, "Semantic end_time needs an event_time property")

	ot = declaredObjectType()
	ot.DataProperties[2].Constraints = &interfaces.PropertyConstraints{ValueType: "amount_cny"}
	require.NoError(t, validateDeclarations(ctx, ot), "a value type is resolved by checkNetworkDeclarations")

	ot = declaredObjectType()
	ot.DataProperties[2].Constraints = &interfaces.PropertyConstraints{ValueType: "amount_cny", Unit: "元"}
	err = validateDeclarations(ctx, ot)
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, "write range, length, pattern and unit in the value type")

	ot = declaredObjectType()
	ot.DataProperties[0] = decodeProperty(t, `{"name":"id","type":"string","required":false}`)
	err = validateDeclarations(ctx, ot)
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `data property "id": a primary key is required by definition`)

	ot = declaredObjectType()
	ot.DataProperties[0] = decodeProperty(t, `{"name":"id","type":"string","required":true}`)
	require.NoError(t, validateDeclarations(ctx, ot))
	assert.False(t, ot.DataProperties[0].Required, "required on a primary key is not stored")
	assert.True(t, ot.DataProperties[1].Required, "required on another property is kept")

	ot = declaredObjectType()
	ot.DataProperties[2] = decodeProperty(t, `{"name":"amount","type":"decimal","constraints":{}}`)
	require.NoError(t, validateDeclarations(ctx, ot))
	assert.Nil(t, ot.DataProperties[2].Constraints, "{} is stored as no constraints")
}

func TestClearedDeclarations(t *testing.T) {
	incoming := declaredObjectType()
	incoming.DataProperties = []*interfaces.DataProperty{
		{Name: "id", Type: "string"},
		{Name: "amount", Type: "decimal", Constraints: &interfaces.PropertyConstraints{Unit: "元"}},
	}
	assert.Equal(t, []interfaces.ClearedDeclaration{
		{ObjectTypeID: "order", Property: "placed_at", Fields: []string{interfaces.DECLARATION_SEMANTIC, interfaces.DECLARATION_REQUIRED}},
	}, clearedDeclarations(declaredObjectType(), incoming),
		"a property the file drops loses its declarations; changed constraints are not cleared; Required on the primary key is implied")

	assert.Empty(t, clearedDeclarations(declaredObjectType(), declaredObjectType()))
}

func TestCheckOverwriteKeepsDeclarations(t *testing.T) {
	ctx := context.Background()
	mockCtrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(mockCtrl)
	ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	service := &objectTypeService{ota: ota}
	file := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", DataProperties: []*interfaces.DataProperty{{Name: "id"}, {Name: "placed_at"}, {Name: "amount"}}},
		KNID:                   "kn1", Branch: interfaces.MAIN_BRANCH,
	}

	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"order"}).
		Return([]*interfaces.ObjectType{declaredObjectType()}, nil)
	err := service.checkOverwriteKeepsDeclarations(ctx, nil, []*interfaces.ObjectType{file}, false)
	require.Error(t, err)
	httpErr := err.(*httperrors.HTTPError)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared, httpErr.ErrorCode)
	details := httpErr.ErrorDetails.(map[string]any)
	assert.Len(t, details["cleared"], 2)
	assert.Equal(t, "confirm_clear_declarations=true", details["confirm_with"])

	// Confirmed, it reads nothing and lets the overwrite through.
	require.NoError(t, service.checkOverwriteKeepsDeclarations(interfaces.WithClearDeclarationsConfirmed(ctx), nil, []*interfaces.ObjectType{file}, false))

	// Nothing stored to clear.
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"order"}).
		Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order"}}}, nil)
	require.NoError(t, service.checkOverwriteKeepsDeclarations(ctx, nil, []*interfaces.ObjectType{file}, false))
}

// Validation checks modify on the network only. Before it lists the declarations an overwrite would
// clear, it checks modify on those object types; with nothing to list it asks nothing more, so an
// overwrite of object types without declarations validates as before.
func TestCheckOverwriteKeepsDeclarationsChecksPermission(t *testing.T) {
	ctx := context.Background()
	mockCtrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(mockCtrl)
	ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ps := bmock.NewMockPermissionService(mockCtrl)
	service := &objectTypeService{ota: ota, ps: ps}
	file := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", DataProperties: []*interfaces.DataProperty{{Name: "id"}}},
		KNID:                   "kn1", Branch: interfaces.MAIN_BRANCH,
	}

	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"order"}).
		Return([]*interfaces.ObjectType{declaredObjectType()}, nil)
	ps.EXPECT().RequirePermissions(gomock.Any(), []interfaces.PermissionRequirement{{
		Resource:  interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ID: interfaces.KNChildResourceID("kn1", "order")},
		Operation: interfaces.OPERATION_TYPE_MODIFY,
	}}).Return(httperrors.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_ObjectType_InvalidParameter))
	err := service.checkOverwriteKeepsDeclarations(ctx, nil, []*interfaces.ObjectType{file}, true)
	require.Error(t, err)
	assert.Equal(t, http.StatusForbidden, err.(*httperrors.HTTPError).HTTPCode, "403, not the list")

	// Nothing stored to clear: no permission call at all.
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"order"}).
		Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order"}}}, nil)
	require.NoError(t, service.checkOverwriteKeepsDeclarations(ctx, nil, []*interfaces.ObjectType{file}, true))
}

// An old client that sends an object type without the declaration fields keeps them; one that
// clears a field with null clears only that field.
func TestUpdateObjectTypeKeepsStoredDeclarations(t *testing.T) {
	ctx := context.Background()
	mockCtrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(mockCtrl)
	ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ps := bmock.NewMockPermissionService(mockCtrl)
	cga := bmock.NewMockConceptGroupAccess(mockCtrl)
	vbs := bmock.NewMockVegaBackendService(mockCtrl)
	db, smock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	service := &objectTypeService{appSetting: &common.AppSetting{}, db: db, ota: ota, ps: ps, cga: cga, vbs: vbs}

	ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("订单", true, nil).AnyTimes()
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	ota.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "order").Return(declaredObjectType(), nil)
	var saved *interfaces.ObjectType
	ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, ot *interfaces.ObjectType) error {
			saved = ot
			return nil
		})
	cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	smock.ExpectBegin()
	smock.ExpectCommit()

	update := declaredObjectType()
	update.DataProperties = []*interfaces.DataProperty{
		decodeProperty(t, `{"name":"id","display_name":"编号","type":"string"}`),
		decodeProperty(t, `{"name":"placed_at","display_name":"下单时间","type":"datetime"}`),
		decodeProperty(t, `{"name":"amount","display_name":"金额","type":"decimal","constraints":null}`),
	}
	require.NoError(t, service.UpdateObjectType(ctx, nil, update, false))

	require.NotNil(t, saved)
	assert.Equal(t, "下单时间", saved.DataProperties[1].DisplayName)
	assert.Equal(t, "event_time", saved.DataProperties[1].Semantic, "left out, so kept")
	assert.False(t, saved.DataProperties[0].Required, "required stored on a primary key is dropped on save")
	assert.True(t, saved.DataProperties[1].Required, "left out, so kept")
	assert.Nil(t, saved.DataProperties[2].Constraints, "null clears")
}

// An overwrite by a caller who may not modify the object type is refused for that, before the
// precheck reads the stored declarations it would list.
func TestOverwritePrecheckRunsAfterThePermissionCheck(t *testing.T) {
	ctx := context.Background()
	mockCtrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(mockCtrl)
	ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ps := bmock.NewMockPermissionService(mockCtrl)
	db, smock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	service := &objectTypeService{appSetting: &common.AppSetting{}, db: db, ota: ota, ps: ps}

	smock.ExpectBegin()
	smock.ExpectRollback()
	ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "order").Return("订单", true, nil)
	ota.EXPECT().CheckObjectTypeExistByName(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "订单").Return("order", true, nil)
	ps.EXPECT().RequirePermissions(gomock.Any(), gomock.Any()).
		Return(httperrors.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_ObjectType_InvalidParameter))
	// No GetObjectTypesByIDs: the stored definition is never read.

	file := declaredObjectType()
	file.DataProperties = []*interfaces.DataProperty{{Name: "id", Type: "string"}}
	_, err = service.CreateObjectTypes(ctx, nil, []*interfaces.ObjectType{file}, interfaces.ImportMode_Overwrite, false, false)
	require.Error(t, err)
	assert.Equal(t, http.StatusForbidden, err.(*httperrors.HTTPError).HTTPCode)
}

// A property may name only a value type of its network, and the value type's base_type must be
// the property's Type.
func TestCheckNetworkDeclarations(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	vta := bmock.NewMockValueTypeAccess(ctrl)
	ots := &objectTypeService{vta: vta}

	require.NoError(t, ots.checkNetworkDeclarations(ctx, declaredObjectType()), "no reference, no lookup")

	amountCNY := &interfaces.ValueType{VTID: "amount_cny", VTName: "人民币金额", BaseType: "decimal"}
	referencing := func() *interfaces.ObjectType {
		ot := declaredObjectType()
		ot.DataProperties[2].Constraints = &interfaces.PropertyConstraints{ValueType: "amount_cny"}
		return ot
	}

	vta.EXPECT().GetValueTypesByIDs(gomock.Any(), nil, "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).
		Return([]*interfaces.ValueType{amountCNY}, nil)
	require.NoError(t, ots.checkNetworkDeclarations(ctx, referencing()))

	// The value type check sees this object type alone: a replaced_by naming another object type is
	// not refused as missing from it.
	replaced := referencing()
	replaced.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "改用采购单", ReplacedBy: "purchase"}
	vta.EXPECT().GetValueTypesByIDs(gomock.Any(), nil, "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).
		Return([]*interfaces.ValueType{amountCNY}, nil)
	require.NoError(t, ots.checkNetworkDeclarations(ctx, replaced))
	assert.Equal(t, "purchase", replaced.Lifecycle.ReplacedBy, "the object type itself keeps its lifecycle")

	vta.EXPECT().GetValueTypesByIDs(gomock.Any(), nil, "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).
		Return([]*interfaces.ValueType{}, nil)
	err := ots.checkNetworkDeclarations(ctx, referencing())
	assert.Equal(t, berrors.BknBackend_ObjectType_InvalidParameter_Declarations, errorCode(t, err))
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `value type "amount_cny" is not defined in this network`)

	vta.EXPECT().GetValueTypesByIDs(gomock.Any(), nil, "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).
		Return([]*interfaces.ValueType{{VTID: "amount_cny", VTName: "人民币金额", BaseType: "integer"}}, nil)
	err = ots.checkNetworkDeclarations(ctx, referencing())
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `value type "amount_cny" has base_type integer`)

	vta.EXPECT().GetValueTypesByIDs(gomock.Any(), nil, "kn1", interfaces.MAIN_BRANCH, []string{"amount_cny"}).
		Return(nil, errors.New("db down"))
	err = ots.checkNetworkDeclarations(ctx, referencing())
	assert.Equal(t, http.StatusInternalServerError, err.(*httperrors.HTTPError).HTTPCode)
}

// decodeObjectType reads an update request the way the handler does, so the object type knows
// whether it carried aliases.
func decodeObjectType(t *testing.T, base *interfaces.ObjectType, aliasesJSON string) *interfaces.ObjectType {
	t.Helper()
	data, err := json.Marshal(base)
	require.NoError(t, err)
	if aliasesJSON != "" {
		data = append(data[:len(data)-1], []byte(`,"aliases":`+aliasesJSON+`}`)...)
	}
	var ot interfaces.ObjectType
	require.NoError(t, json.Unmarshal(data, &ot))
	ot.KNID, ot.Branch = base.KNID, base.Branch
	return &ot
}

// An interactive update keeps the stored aliases of the object type when it leaves them out, and
// clears them with []; aliases are checked against the names of the other object types.
func TestUpdateObjectTypeKeepsStoredAliases(t *testing.T) {
	stored := declaredObjectType()
	stored.Aliases = []string{"订货单"}

	run := func(t *testing.T, aliasesJSON string) (*interfaces.ObjectType, *bmock.MockObjectTypeAccess) {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		ps := bmock.NewMockPermissionService(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		db, smock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		service := &objectTypeService{appSetting: &common.AppSetting{}, db: db, ota: ota, ps: ps, cga: cga, vbs: vbs}

		ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("订单", true, nil).AnyTimes()
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ota.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "order").Return(stored, nil)
		var saved *interfaces.ObjectType
		ota.EXPECT().UpdateObjectType(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, ot *interfaces.ObjectType) error {
				saved = ot
				return nil
			})
		cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string][]*interfaces.ConceptGroup{}, nil)
		vbs.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		smock.ExpectBegin()
		smock.ExpectCommit()
		if aliasesJSON == "" {
			ota.EXPECT().GetObjectTypeIdentitiesByIDsOrNames(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, nil, []string{"订货单"}).
				Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单"}}}, nil)
		}
		require.NoError(t, service.UpdateObjectType(ctx, nil, decodeObjectType(t, declaredObjectType(), aliasesJSON), false))
		return saved, ota
	}

	saved, _ := run(t, "")
	assert.Equal(t, []string{"订货单"}, saved.Aliases, "left out, so kept")

	saved, _ = run(t, "[]")
	assert.Nil(t, saved.Aliases, "[] clears")
	saved, _ = run(t, "null")
	assert.Nil(t, saved.Aliases, "null clears")
}

func TestClearedDeclarationsListsAliases(t *testing.T) {
	stored := declaredObjectType()
	stored.Aliases = []string{"订货单"}
	stored.DataProperties[2].Aliases = []string{"价款"}
	assert.Equal(t, []interfaces.ClearedDeclaration{
		{ObjectTypeID: "order", Fields: []string{interfaces.DECLARATION_ALIASES}},
		{ObjectTypeID: "order", Property: "amount", Fields: []string{interfaces.DECLARATION_ALIASES}},
	}, clearedDeclarations(stored, declaredObjectType()))

	incoming := declaredObjectType()
	incoming.Aliases = []string{"采购单"}
	incoming.DataProperties[2].Aliases = []string{"金额"}
	assert.Empty(t, clearedDeclarations(stored, incoming), "changed aliases are not cleared")
}

func TestCheckAliasNames(t *testing.T) {
	ctx := context.Background()
	named := func(id, name string, aliases ...string) *interfaces.ObjectType {
		return &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, OTName: name}, Aliases: aliases}
	}

	t.Run("an alias naming another object type", func(t *testing.T) {
		ota := bmock.NewMockObjectTypeAccess(gomock.NewController(t))
		ots := &objectTypeService{ota: ota}
		ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(nil, nil)
		ota.EXPECT().GetObjectTypeIdentitiesByIDsOrNames(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, nil, []string{"订货单", "客户"}).
			Return([]*interfaces.ObjectType{named("customer", "客户")}, nil)
		err := ots.checkAliasNames(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ObjectType{named("order", "订单", "订货单", "客户")})
		assert.Equal(t, berrors.BknBackend_ObjectType_InvalidParameter_Declarations, errorCode(t, err))
		assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `object type "order" alias "客户" is the name of object type "customer"`)
	})

	t.Run("a new object type named like a stored alias", func(t *testing.T) {
		ota := bmock.NewMockObjectTypeAccess(gomock.NewController(t))
		ots := &objectTypeService{ota: ota}
		ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).
			Return([]*interfaces.ObjectType{named("order", "订单", "客户")}, nil)
		err := ots.checkAliasNames(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ObjectType{named("customer", "客户")})
		assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `object type "customer" is named "客户", which is an alias of object type "order"`)
	})

	t.Run("without aliases only the aliased object types are read", func(t *testing.T) {
		ota := bmock.NewMockObjectTypeAccess(gomock.NewController(t))
		ots := &objectTypeService{ota: ota}
		ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(nil, nil)
		require.NoError(t, ots.checkAliasNames(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ObjectType{declaredObjectType()}))
	})

	ot := declaredObjectType()
	ot.Aliases = []string{"订单"}
	err := validateDeclarations(ctx, ot)
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, "订单", "an alias is not the object type's own name")
}

// The vector text has the aliases right after the name; without aliases it is as before.
func TestObjectTypeVectorTextCarriesAliases(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mfs := bmock.NewMockModelFactoryService(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	ots := &objectTypeService{
		appSetting: &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}},
		mfs:        mfs, vbs: vbs,
	}
	plain := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单"},
		CommonInfo: interfaces.CommonInfo{Tags: []string{"交易"}, Comment: "客户订单", BKNRawContent: "raw"}, KNID: "kn1"}
	aliased := *plain
	aliased.Aliases = []string{"订货单", "PO"}

	mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(&interfaces.SmallModel{ModelID: "m"}, nil)
	mfs.EXPECT().GetVector(gomock.Any(), gomock.Any(), []string{
		"订单\n交易\n客户订单\nraw",
		"订单\n订货单\nPO\n交易\n客户订单\nraw",
	}).Return([]*cond.VectorResp{{Vector: []float32{1}}, {Vector: []float32{1}}}, nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	require.NoError(t, ots.InsertDatasetData(ctx, []*interfaces.ObjectType{plain, &aliased}))
}

// With ignore, an object type that already exists is skipped and keeps its stored name and
// aliases, so it does not count as saved: a new object type named like its stored alias is refused.
func TestValidateObjectTypesIgnoreKeepsStoredAliasesInTheCheck(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := &objectTypeService{ota: ota, ps: ps}

	order := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单"}, KNID: "kn1", Branch: interfaces.MAIN_BRANCH}
	customer := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "customer", OTName: "客户"}, KNID: "kn1", Branch: interfaces.MAIN_BRANCH}
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	ota.EXPECT().GetObjectTypeIdentitiesByIDsOrNames(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, gomock.Any(), gomock.Any()).
		Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单"}}}, nil)
	ota.EXPECT().ListAliasedObjectTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).
		Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单"}, Aliases: []string{"客户"}}}, nil)

	err := ots.ValidateObjectTypes(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ObjectType{order, customer}, false, nil, interfaces.ImportMode_Ignore)
	assert.Equal(t, berrors.BknBackend_ObjectType_InvalidParameter_Declarations, errorCode(t, err))
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `object type "customer" is named "客户", which is an alias of object type "order"`)
}

// During an import the value types it writes resolve before they are committed, and replace the
// stored ones of the same ID; the others are read from storage.
func TestCheckNetworkDeclarationsResolvesPendingValueTypes(t *testing.T) {
	vta := bmock.NewMockValueTypeAccess(gomock.NewController(t))
	ots := &objectTypeService{vta: vta}
	ot := declaredObjectType()
	ot.DataProperties[0].Constraints = &interfaces.PropertyConstraints{ValueType: "order_no"}
	ot.DataProperties[2].Constraints = &interfaces.PropertyConstraints{ValueType: "amount_cny"}

	ctx := interfaces.WithPendingValueTypes(context.Background(), []*interfaces.ValueType{
		{VTID: "amount_cny", VTName: "人民币金额", BaseType: "decimal"},
	})
	vta.EXPECT().GetValueTypesByIDs(gomock.Any(), nil, "kn1", interfaces.MAIN_BRANCH, []string{"order_no"}).
		Return([]*interfaces.ValueType{{VTID: "order_no", VTName: "订单号", BaseType: "string"}}, nil)
	require.NoError(t, ots.checkNetworkDeclarations(ctx, ot))

	ctx = interfaces.WithPendingValueTypes(context.Background(), []*interfaces.ValueType{
		{VTID: "amount_cny", VTName: "人民币金额", BaseType: "integer"},
		{VTID: "order_no", VTName: "订单号", BaseType: "string"},
	})
	err := ots.checkNetworkDeclarations(ctx, ot)
	assert.Contains(t, err.(*httperrors.HTTPError).ErrorDetails, `value type "amount_cny" has base_type integer`,
		"the import's version is checked, nothing read")
}

// The lifecycle of an object type is a declaration of the object type itself: an interactive update
// keeps it when left out and clears it on null; an overwrite without it lists it as cleared.
func TestObjectTypeLifecycleDeclaration(t *testing.T) {
	deprecated := &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "改用采购单"}
	stored := declaredObjectType()
	stored.Lifecycle = deprecated

	decode := func(body string) *interfaces.ObjectType {
		var ot interfaces.ObjectType
		require.NoError(t, json.Unmarshal([]byte(body), &ot))
		return &ot
	}
	update := decode(`{"id":"order","name":"订单"}`)
	keepUndeclared(update, stored)
	assert.Equal(t, deprecated, update.Lifecycle, "left out, so kept")
	update = decode(`{"id":"order","name":"订单","lifecycle":null}`)
	keepUndeclared(update, stored)
	assert.Nil(t, update.Lifecycle, "null clears")

	assert.Equal(t, []interfaces.ClearedDeclaration{{ObjectTypeID: "order", Fields: []string{interfaces.DECLARATION_LIFECYCLE}}},
		clearedDeclarations(stored, declaredObjectType()))
	incoming := declaredObjectType()
	incoming.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "另一个原因"}
	assert.Empty(t, clearedDeclarations(stored, incoming))

	ctx := context.Background()
	active := declaredObjectType()
	active.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_ACTIVE}
	require.NoError(t, validateDeclarations(ctx, active))
	assert.Nil(t, active.Lifecycle, "status active is stored as no lifecycle")
	noReason := declaredObjectType()
	noReason.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED}
	assert.Error(t, validateDeclarations(ctx, noReason))
}
