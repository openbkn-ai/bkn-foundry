// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestActionTypeSingleResourcePEP(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		invoke    func(*actionTypeService, context.Context) error
	}{
		{"detail", interfaces.OPERATION_TYPE_VIEW_DETAIL, func(service *actionTypeService, ctx context.Context) error {
			_, err := service.GetActionTypesByIDs(ctx, "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"})
			return err
		}},
		{"update", interfaces.OPERATION_TYPE_MODIFY, func(service *actionTypeService, ctx context.Context) error {
			return service.UpdateActionType(ctx, nil, &interfaces.ActionType{
				ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at-1"},
				KNID:                   "kn-1", Branch: interfaces.MAIN_BRANCH,
			}, false)
		}},
		{"delete", interfaces.OPERATION_TYPE_DELETE, func(service *actionTypeService, ctx context.Context) error {
			return service.DeleteActionTypesByIDs(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ata := bmock.NewMockActionTypeAccess(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)
			denied := errors.New("denied")
			if tt.name == "detail" {
				ata.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"}).
					Return([]*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at-1"}}}, nil)
			} else {
				ata.EXPECT().CheckActionTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "at-1").
					Return("action", true, nil)
			}
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_ACTION_TYPE, ID: "kn-1/at-1",
			}, []string{tt.operation}).Return(denied)
			service := &actionTypeService{ata: ata, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}
