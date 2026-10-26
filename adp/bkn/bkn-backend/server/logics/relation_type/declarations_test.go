// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package relation_type

import (
	"context"
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
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func declHTTPError(t *testing.T, err error) *httperrors.HTTPError {
	t.Helper()
	var httpErr *httperrors.HTTPError
	require.True(t, errors.As(err, &httpErr), "%v", err)
	return httpErr
}

// workplace returns an indirect relation from employee to department through the employment table.
func workplace() *interfaces.RelationType {
	return &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "works_in", RTName: "任职于", SourceObjectTypeID: "employee", TargetObjectTypeID: "dept",
			Type: interfaces.RELATION_TYPE_INDIRECT,
			MappingRules: &interfaces.InDirectMapping{
				BackingDataSource:  &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "res_employment"},
				SourceMappingRules: []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "id"}, TargetProp: interfaces.SimpleProperty{Name: "emp_id"}}},
				TargetMappingRules: []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "dept_id"}, TargetProp: interfaces.SimpleProperty{Name: "id"}}},
			},
		},
		KNID: "kn1", Branch: interfaces.MAIN_BRANCH,
	}
}

func withProperties(rt *interfaces.RelationType, props ...*interfaces.DataProperty) *interfaces.RelationType {
	rt.MappingRules.(*interfaces.InDirectMapping).Properties = props
	return rt
}

func titleProperty() *interfaces.DataProperty {
	return &interfaces.DataProperty{Name: "title", DisplayName: "职务", Type: "string", MappedField: &interfaces.Field{Name: "title"}}
}

// A save checks the relation declarations with the checks a 3.0 import runs, the relation property
// rows included, and stores the explicit defaults as empty.
func TestValidateRelationDeclarations(t *testing.T) {
	ctx := context.Background()

	rt := withProperties(workplace(), titleProperty())
	rt.ReverseName, rt.Cardinality, rt.Verification = "雇员", "many_to_one", "confirmed"
	rt.Importance = interfaces.IMPORTANCE_PRIMARY
	rt.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_ACTIVE}
	rt.Constraints = &interfaces.RelationConstraints{}
	require.NoError(t, validateDeclarations(ctx, rt))
	assert.Empty(t, rt.Importance, "importance primary is stored as empty")
	assert.Nil(t, rt.Lifecycle, "status active is stored as no lifecycle")
	assert.Nil(t, rt.Constraints, "empty constraints are stored as none")

	refused := func(rt *interfaces.RelationType, detail string) {
		t.Helper()
		httpErr := declHTTPError(t, validateDeclarations(ctx, rt))
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, berrors.BknBackend_RelationType_InvalidParameter_Declarations, httpErr.ErrorCode)
		assert.Contains(t, httpErr.ErrorDetails, detail)
	}
	rt = workplace()
	rt.FallbackFor = "works_in_direct"
	refused(rt, "fallback_for goes with importance: secondary")

	rt = workplace()
	rt.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED}
	refused(rt, "status deprecated needs a reason")

	bad := titleProperty()
	bad.Name = "Title!"
	refused(withProperties(workplace(), bad), `relation property: property name "Title!" must match`)

	direct := workplace()
	direct.Type = interfaces.RELATION_TYPE_DIRECT
	direct.MappingRules = []interfaces.Mapping{{SourceProp: interfaces.SimpleProperty{Name: "dept_id"}, TargetProp: interfaces.SimpleProperty{Name: "id"}}}
	direct.Constraints = &interfaces.RelationConstraints{Characteristics: []string{"symmetric"}}
	refused(direct, "characteristics apply to a relation whose Source and Target are the same object type")
}

// An overwrite that would clear stored relation declarations is refused with what it would clear,
// unless confirmed.
func TestCheckRelationTypeOverwriteKeepsDeclarations(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	rts := &relationTypeService{rta: rta, ps: ps}

	stored := withProperties(workplace(), titleProperty())
	stored.ReverseName = "雇员"
	stored.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "改用任职记录"}
	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"works_in"}).
		Return([]*interfaces.RelationType{stored}, nil).Times(3)

	err := rts.checkOverwriteKeepsDeclarations(ctx, []*interfaces.RelationType{workplace()}, false)
	httpErr := declHTTPError(t, err)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, berrors.BknBackend_RelationType_DeclarationsWouldBeCleared, httpErr.ErrorCode)
	assert.Equal(t, []interfaces.ClearedDeclaration{{RelationTypeID: "works_in", Fields: []string{
		interfaces.DECLARATION_REVERSE_NAME, interfaces.DECLARATION_LIFECYCLE, interfaces.DECLARATION_PROPERTIES,
	}}}, httpErr.ErrorDetails.(map[string]any)["cleared"])

	incoming := withProperties(workplace(), titleProperty())
	incoming.ReverseName = "员工"
	incoming.Lifecycle = &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "另一个原因"}
	require.NoError(t, rts.checkOverwriteKeepsDeclarations(ctx, []*interfaces.RelationType{incoming}, false),
		"changed declarations are not cleared")

	ps.EXPECT().RequirePermissions(gomock.Any(), gomock.Any()).
		Return(httperrors.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_RelationType_InternalError))
	err = rts.checkOverwriteKeepsDeclarations(ctx, []*interfaces.RelationType{workplace()}, true)
	assert.Equal(t, http.StatusForbidden, declHTTPError(t, err).HTTPCode, "the list is shown only to who may modify")

	require.NoError(t, rts.checkOverwriteKeepsDeclarations(interfaces.WithClearDeclarationsConfirmed(ctx),
		[]*interfaces.RelationType{workplace()}, false), "confirmed, nothing read")
}

// An interactive update keeps the stored declarations its request leaves out and replaces the ones it
// carries, relation properties included.
func TestUpdateRelationTypeKeepsUndeclared(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	rts := &relationTypeService{appSetting: &common.AppSetting{}, db: db, rta: rta, ps: ps, ots: ots, vbs: vbs}

	stored := withProperties(workplace(), titleProperty())
	stored.ReverseName, stored.Verification = "雇员", "unconfirmed"
	rta.EXPECT().CheckRelationTypeExistByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "works_in").Return("任职于", true, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	rta.EXPECT().GetRelationTypeByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "works_in").Return(stored, nil)
	ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&interfaces.ObjectType{}, nil).AnyTimes()
	var written *interfaces.RelationType
	rta.EXPECT().UpdateRelationType(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ any, rt *interfaces.RelationType) error { written = rt; return nil })
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	smock.ExpectBegin()
	smock.ExpectCommit()

	var update interfaces.RelationType
	require.NoError(t, json.Unmarshal([]byte(`{"id":"works_in","name":"任职","source_object_type_id":"employee",
		"target_object_type_id":"dept","type":"indirect","verification":null}`), &update))
	update.KNID, update.Branch = "kn1", interfaces.MAIN_BRANCH
	update.MappingRules = workplace().MappingRules
	require.NoError(t, rts.UpdateRelationType(ctx, nil, &update, false))

	require.NotNil(t, written)
	assert.Equal(t, "雇员", written.ReverseName, "left out, so kept")
	assert.Empty(t, written.Verification, "carried as null, so cleared")
	assert.Equal(t, []*interfaces.DataProperty{titleProperty()}, written.RelationProperties(), "left out, so kept")
}

// In strict mode a relation property must map a column of the backing resource, like the mapping
// rules; a property kept from before a change of resource that names a column the new one lacks is
// refused.
func TestStrictRelationPropertyMapsABackingColumn(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	vbs.EXPECT().GetResourceSchema(gomock.Any(), "res_employment", interfaces.OPERATION_TYPE_QUERY_DATA).
		Return(&interfaces.VegaResource{ID: "res_employment", Name: "任职表", SchemaDefinition: []*interfaces.Property{
			{Name: "emp_id"}, {Name: "dept_id"}, {Name: "title"},
		}}, nil).Times(2)
	rts := &relationTypeService{vbs: vbs}
	relation := func(mapped string) *interfaces.RelationType {
		rt := withProperties(workplace(), titleProperty())
		rt.SourceObjectTypeID, rt.TargetObjectTypeID = "", ""
		rt.RelationProperties()[0].MappedField = &interfaces.Field{Name: mapped}
		return rt
	}

	require.NoError(t, rts.validateDependency(ctx, nil, relation("title"), true, nil))

	httpErr := declHTTPError(t, rts.validateDependency(ctx, nil, relation("position"), true, nil))
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Equal(t, berrors.BknBackend_RelationType_InvalidParameter, httpErr.ErrorCode)
	assert.Contains(t, httpErr.ErrorDetails, "position")

	require.NoError(t, rts.validateDependency(ctx, nil, relation("position"), false, nil), "not checked outside strict mode")
}
