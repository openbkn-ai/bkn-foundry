// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func approve(aliases ...string) *interfaces.ActionType {
	return &interfaces.ActionType{
		ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "approve", ATName: "审批"},
		Aliases:                aliases,
		KNID:                   "kn1", Branch: interfaces.MAIN_BRANCH,
	}
}

func httpError(t *testing.T, err error) *rest.HTTPError {
	t.Helper()
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr), "%v", err)
	return httpErr
}

func TestCheckActionTypeDeclarations(t *testing.T) {
	ctx := context.Background()
	ats := &actionTypeService{}

	at := approve()
	at.Aliases = []string{}
	require.NoError(t, ats.checkDeclarations(ctx, at))
	assert.Nil(t, at.Aliases, "[] is stored as no aliases")

	err := ats.checkDeclarations(ctx, approve("审批"))
	assert.Equal(t, berrors.BknBackend_ActionType_InvalidParameter_Declarations, httpError(t, err).BaseError.ErrorCode,
		"an alias is not the action type's own name")
}

func TestCheckActionTypeAliasNames(t *testing.T) {
	ctx := context.Background()
	reject := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "reject", ATName: "驳回"}}

	ata := bmock.NewMockActionTypeAccess(gomock.NewController(t))
	ats := &actionTypeService{ata: ata}
	ata.EXPECT().ListAliasedActionTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(nil, nil)
	ata.EXPECT().GetActionTypeIdentitiesByIDsOrNames(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, nil, []string{"核准", "驳回"}).
		Return([]*interfaces.ActionType{reject}, nil)
	err := ats.checkAliasNames(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ActionType{approve("核准", "驳回")})
	assert.Contains(t, httpError(t, err).BaseError.ErrorDetails, `action type "approve" alias "驳回" is the name of action type "reject"`)

	ata.EXPECT().ListAliasedActionTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).
		Return([]*interfaces.ActionType{approve("驳回")}, nil)
	err = ats.checkAliasNames(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ActionType{reject})
	assert.Contains(t, httpError(t, err).BaseError.ErrorDetails, `action type "reject" is named "驳回", which is an alias of action type "approve"`)

	ata.EXPECT().ListAliasedActionTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).
		Return([]*interfaces.ActionType{approve("驳回")}, nil)
	require.NoError(t, ats.checkAliasNames(ctx, "kn1", interfaces.MAIN_BRANCH, []*interfaces.ActionType{approve(), reject}),
		"dropping the alias in the same save frees the name")
}

// An interactive update keeps the stored aliases only when the request left them out.
func TestKeepStoredAliases(t *testing.T) {
	ctx := context.Background()
	ata := bmock.NewMockActionTypeAccess(gomock.NewController(t))
	ats := &actionTypeService{ata: ata}
	decode := func(request string) *interfaces.ActionType {
		var at interfaces.ActionType
		require.NoError(t, json.Unmarshal([]byte(request), &at))
		at.KNID, at.Branch = "kn1", interfaces.MAIN_BRANCH
		return &at
	}

	ata.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"approve"}).
		Return([]*interfaces.ActionType{approve("核准")}, nil)
	at := decode(`{"id":"approve","name":"审批（新）"}`)
	require.NoError(t, ats.keepStoredAliases(ctx, at))
	assert.Equal(t, []string{"核准"}, at.Aliases, "left out, so kept")

	at = decode(`{"id":"approve","name":"审批","aliases":[]}`)
	require.NoError(t, ats.keepStoredAliases(ctx, at), "carried, so nothing is read")
	assert.Empty(t, at.Aliases)
}

func TestCheckActionTypeOverwriteKeepsDeclarations(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ats := &actionTypeService{ata: ata, ps: ps}

	ata.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"approve"}).
		Return([]*interfaces.ActionType{approve("核准")}, nil).Times(3)

	err := ats.checkOverwriteKeepsDeclarations(ctx, []*interfaces.ActionType{approve()}, false)
	httpErr := httpError(t, err)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, berrors.BknBackend_ActionType_DeclarationsWouldBeCleared, httpErr.BaseError.ErrorCode)
	assert.Equal(t, []interfaces.ClearedDeclaration{{ActionTypeID: "approve", Fields: []string{interfaces.DECLARATION_ALIASES}}},
		httpErr.BaseError.ErrorDetails.(map[string]any)["cleared"])

	require.NoError(t, ats.checkOverwriteKeepsDeclarations(ctx, []*interfaces.ActionType{approve("批准")}, false),
		"changed aliases are not cleared")

	ps.EXPECT().RequirePermissions(gomock.Any(), gomock.Any()).
		Return(rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_ActionType_InternalError))
	err = ats.checkOverwriteKeepsDeclarations(ctx, []*interfaces.ActionType{approve()}, true)
	assert.Equal(t, http.StatusForbidden, httpError(t, err).HTTPCode, "the list is shown only to who may modify")

	require.NoError(t, ats.checkOverwriteKeepsDeclarations(interfaces.WithClearDeclarationsConfirmed(ctx),
		[]*interfaces.ActionType{approve()}, false), "confirmed, nothing read")
}

// The vector text has the aliases right after the name; without aliases it is as before.
func TestActionTypeVectorTextCarriesAliases(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mfs := bmock.NewMockModelFactoryService(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	ats := &actionTypeService{
		appSetting: &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}},
		mfs:        mfs, vbs: vbs,
	}
	plain := approve()
	plain.Tags, plain.Comment, plain.BKNRawContent = []string{"流程"}, "审批单据", "raw"
	aliased := approve("核准", "批准")
	aliased.Tags, aliased.Comment, aliased.BKNRawContent = plain.Tags, plain.Comment, plain.BKNRawContent

	mfs.EXPECT().GetDefaultModel(gomock.Any()).Return(&interfaces.SmallModel{ModelID: "m"}, nil)
	mfs.EXPECT().GetVector(gomock.Any(), gomock.Any(), []string{
		"审批\n流程\n审批单据\nraw",
		"审批\n核准\n批准\n流程\n审批单据\nraw",
	}).Return([]*cond.VectorResp{{Vector: []float32{1}}, {Vector: []float32{1}}}, nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	require.NoError(t, ats.InsertDatasetData(ctx, []*interfaces.ActionType{plain, aliased}))
}
