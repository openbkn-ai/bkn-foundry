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
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
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
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr), "%v", err)
	return httpErr.BaseError.ErrorCode
}

func decodeProperty(t *testing.T, text string) *interfaces.DataProperty {
	t.Helper()
	var p interfaces.DataProperty
	require.NoError(t, json.Unmarshal([]byte(text), &p))
	return &p
}

// A save runs the checks a 3.0 import runs, plus what the backend cannot store yet.
func TestValidateDeclarations(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, validateDeclarations(ctx, declaredObjectType()))

	ot := declaredObjectType()
	ot.DataProperties[1].Semantic = "end_time"
	err := validateDeclarations(ctx, ot)
	assert.Equal(t, berrors.BknBackend_ObjectType_InvalidParameter_Declarations, errorCode(t, err))
	assert.Contains(t, err.(*rest.HTTPError).BaseError.ErrorDetails, "Semantic end_time needs an event_time property")

	ot = declaredObjectType()
	ot.DataProperties[2].Constraints = &interfaces.PropertyConstraints{ValueType: "amount_cny"}
	err = validateDeclarations(ctx, ot)
	assert.Contains(t, err.(*rest.HTTPError).BaseError.ErrorDetails, "value_type is not supported by this version yet")

	ot = declaredObjectType()
	ot.DataProperties[0] = decodeProperty(t, `{"name":"id","type":"string","required":false}`)
	err = validateDeclarations(ctx, ot)
	assert.Contains(t, err.(*rest.HTTPError).BaseError.ErrorDetails, `data property "id": a primary key is required by definition`)

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
	service := &objectTypeService{ota: ota}
	file := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", DataProperties: []*interfaces.DataProperty{{Name: "id"}, {Name: "placed_at"}, {Name: "amount"}}},
		KNID:                   "kn1", Branch: interfaces.MAIN_BRANCH,
	}

	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"order"}).
		Return([]*interfaces.ObjectType{declaredObjectType()}, nil)
	err := service.checkOverwriteKeepsDeclarations(ctx, nil, []*interfaces.ObjectType{file})
	require.Error(t, err)
	httpErr := err.(*rest.HTTPError)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared, httpErr.BaseError.ErrorCode)
	details := httpErr.BaseError.ErrorDetails.(map[string]any)
	assert.Len(t, details["cleared"], 2)
	assert.Equal(t, "confirm_clear_declarations=true", details["confirm_with"])

	// Confirmed, it reads nothing and lets the overwrite through.
	require.NoError(t, service.checkOverwriteKeepsDeclarations(interfaces.WithClearDeclarationsConfirmed(ctx), nil, []*interfaces.ObjectType{file}))

	// Nothing stored to clear.
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"order"}).
		Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order"}}}, nil)
	require.NoError(t, service.checkOverwriteKeepsDeclarations(ctx, nil, []*interfaces.ObjectType{file}))
}

// An old client that sends an object type without the declaration fields keeps them; one that
// clears a field with null clears only that field.
func TestUpdateObjectTypeKeepsStoredDeclarations(t *testing.T) {
	ctx := context.Background()
	mockCtrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(mockCtrl)
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
	assert.True(t, saved.DataProperties[1].Required, "left out, so kept")
	assert.Nil(t, saved.DataProperties[2].Constraints, "null clears")
}
