// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestRelationTypeSingleResourcePEP(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
	tests := []struct {
		name      string
		operation string
		invoke    func(*relationTypeService, context.Context) error
	}{
		{"detail", interfaces.OPERATION_TYPE_VIEW_DETAIL, func(service *relationTypeService, ctx context.Context) error {
			_, err := service.GetRelationTypesByIDs(ctx, "kn-1", interfaces.MAIN_BRANCH, []string{"rt-1"})
			return err
		}},
		{"update", interfaces.OPERATION_TYPE_MODIFY, func(service *relationTypeService, ctx context.Context) error {
			return service.UpdateRelationType(ctx, nil, &interfaces.RelationType{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt-1"},
				KNID:                     "kn-1", Branch: interfaces.MAIN_BRANCH,
			}, false)
		}},
		{"delete", interfaces.OPERATION_TYPE_DELETE, func(service *relationTypeService, ctx context.Context) error {
			return service.DeleteRelationTypesByIDs(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, []string{"rt-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rta := bmock.NewMockRelationTypeAccess(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)
			denied := errors.New("denied")
			if tt.name == "detail" {
				rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"rt-1"}).
					Return([]*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt-1"}}}, nil)
			} else {
				rta.EXPECT().CheckRelationTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "rt-1").
					Return("relation", true, nil)
			}
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_RELATION_TYPE, ID: "kn-1/rt-1",
			}, []string{tt.operation}).Return(denied)
			service := &relationTypeService{rta: rta, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}
