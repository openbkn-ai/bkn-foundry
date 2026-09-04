// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package relation_type

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestRelationTypeSingleResourceAuthorization(t *testing.T) {
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
			if tt.name == "detail" {
				ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
					[]string{"kn-1/rt-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).Return(nil, denied)
			} else {
				ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
					Type: interfaces.RESOURCE_TYPE_RELATION_TYPE, ID: "kn-1/rt-1",
				}, []string{tt.operation}).Return(denied)
			}
			service := &relationTypeService{rta: rta, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}

func TestRelationTypeMultiResourceDetailRequiresEveryChildPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ids := []string{"rt-1", "rt-2"}

	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, ids).
		Return([]*interfaces.RelationType{
			{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt-1"}},
			{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt-2"}},
		}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE,
		[]string{"kn-1/rt-1", "kn-1/rt-2"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/rt-1": {ResourceID: "kn-1/rt-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)

	service := &relationTypeService{rta: rta, ps: ps}
	_, err := service.GetRelationTypesByIDs(context.Background(), "kn-1", interfaces.MAIN_BRANCH, ids)
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("GetRelationTypesByIDs() error = %v, want 403 when any child is denied", err)
	}
}
