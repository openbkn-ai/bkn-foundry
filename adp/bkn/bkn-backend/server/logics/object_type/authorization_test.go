// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestObjectTypeSingleResourceAuthorization(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		invoke    func(*objectTypeService, context.Context) error
	}{
		{"detail", interfaces.OPERATION_TYPE_VIEW_DETAIL, func(service *objectTypeService, ctx context.Context) error {
			_, err := service.GetObjectTypesByIDs(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, []string{"ot-1"})
			return err
		}},
		{"update", interfaces.OPERATION_TYPE_MODIFY, func(service *objectTypeService, ctx context.Context) error {
			return service.UpdateObjectType(ctx, nil, &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"},
				KNID:                   "kn-1", Branch: interfaces.MAIN_BRANCH,
			}, false)
		}},
		{"delete", interfaces.OPERATION_TYPE_DELETE, func(service *objectTypeService, ctx context.Context) error {
			return service.DeleteObjectTypesByIDs(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, []string{"ot-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ota := bmock.NewMockObjectTypeAccess(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)
			denied := errors.New("denied")
			service := &objectTypeService{ota: ota, ps: ps}
			if tt.name == "detail" {
				db, sqlMock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				service.db = db
				sqlMock.ExpectBegin()
				ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"ot-1"}).
					Return([]*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"}}}, nil)
				sqlMock.ExpectRollback()
			} else {
				ota.EXPECT().CheckObjectTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "ot-1").
					Return("object", true, nil)
			}
			if tt.name == "detail" {
				ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
					[]string{"kn-1/ot-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).Return(nil, denied)
			} else {
				ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
					Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ID: "kn-1/ot-1",
				}, []string{tt.operation}).Return(denied)
			}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}

func TestObjectTypeMultiResourceDetailRequiresEveryChildPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	db, sqlMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ids := []string{"ot-1", "ot-2"}
	sqlMock.ExpectBegin()
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, ids).
		Return([]*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-2"}},
		}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/ot-1", "kn-1/ot-2"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/ot-1": {ResourceID: "kn-1/ot-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)
	sqlMock.ExpectRollback()

	service := &objectTypeService{db: db, ota: ota, ps: ps}
	_, err = service.GetObjectTypesByIDs(context.Background(), nil, "kn-1", interfaces.MAIN_BRANCH, ids)
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("GetObjectTypesByIDs() error = %v, want 403 when any child is denied", err)
	}
	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
