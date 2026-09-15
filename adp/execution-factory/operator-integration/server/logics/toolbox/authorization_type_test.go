package toolbox

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestFunctionBoxExecuteRequiresFunctionGrantAndProxyType(t *testing.T) {
	ctrl := gomock.NewController(t)
	boxDB := mocks.NewMockIToolboxDB(ctrl)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	accessor := &interfaces.AuthAccessor{ID: "caller-1"}
	boxDB.EXPECT().SelectToolBox(gomock.Any(), "box-fn").Return(true, &model.ToolboxDB{
		BoxID: "box-fn", MetadataType: string(interfaces.MetadataTypeFunc),
	}, nil).Times(2)
	auth.EXPECT().CheckExecutePermission(gomock.Any(), accessor, "box-fn", interfaces.AuthResourceTypeFunction).Return(nil)
	svc := &ToolServiceImpl{ToolBoxDB: boxDB, AuthService: auth}
	ctx := interfaces.WithProxyExecutionContext(context.Background(), interfaces.ProxyExecutionContext{
		TargetType: interfaces.ProxyTargetTypeFunction, TargetID: "box-fn",
	})
	if err := svc.checkBoxExecutePermission(ctx, accessor, "box-fn"); err != nil { t.Fatal(err) }
	wrong := interfaces.WithProxyExecutionContext(context.Background(), interfaces.ProxyExecutionContext{
		TargetType: interfaces.ProxyTargetTypeToolBox, TargetID: "box-fn",
	})
	if err := svc.checkBoxExecutePermission(wrong, accessor, "box-fn"); err == nil {
		t.Fatal("API proxy target executed a function box")
	}
}

func TestAPIWildcardDoesNotListFunctionBoxes(t *testing.T) {
	ctrl := gomock.NewController(t)
	boxDB := mocks.NewMockIToolboxDB(ctrl)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	accessor := &interfaces.AuthAccessor{ID: "caller-1"}
	auth.EXPECT().ResourceListIDs(gomock.Any(), accessor, interfaces.AuthResourceTypeToolBox, interfaces.AuthOperationTypeView).
		Return([]string{interfaces.ResourceIDAll}, nil)
	boxDB.EXPECT().SelectToolBoxList(gomock.Any(), gomock.Any(), nil, nil).Return([]*model.ToolboxDB{
		{BoxID: "box-api", MetadataType: string(interfaces.MetadataTypeAPI)},
		{BoxID: "box-fn", MetadataType: string(interfaces.MetadataTypeFunc)},
	}, nil)
	auth.EXPECT().ResourceFilterIDs(gomock.Any(), accessor, []string{"box-api"}, interfaces.AuthResourceTypeToolBox,
		interfaces.AuthOperationTypeView).Return([]string{"box-api"}, nil)
	auth.EXPECT().ResourceListIDs(gomock.Any(), accessor, interfaces.AuthResourceTypeFunction, interfaces.AuthOperationTypeView).
		Return(nil, nil)
	ids, err := (&ToolServiceImpl{ToolBoxDB: boxDB, AuthService: auth}).authorizedBoxIDs(context.Background(), accessor, "", interfaces.AuthOperationTypeView)
	if err != nil || len(ids) != 1 || ids[0] != "box-api" { t.Fatalf("viewable IDs = %#v, %v", ids, err) }
}
