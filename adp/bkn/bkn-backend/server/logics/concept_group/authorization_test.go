// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package concept_group

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestConceptGroupSingleResourceAuthorization(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		invoke    func(*conceptGroupService, context.Context) error
	}{
		{
			name: "detail", operation: interfaces.OPERATION_TYPE_VIEW_DETAIL,
			invoke: func(service *conceptGroupService, ctx context.Context) error {
				_, err := service.GetConceptGroupByID(ctx, "kn-1", interfaces.MAIN_BRANCH, "cg-1", "")
				return err
			},
		},
		{
			name: "update", operation: interfaces.OPERATION_TYPE_MODIFY,
			invoke: func(service *conceptGroupService, ctx context.Context) error {
				return service.UpdateConceptGroup(ctx, nil, &interfaces.ConceptGroup{
					CGID: "cg-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
				}, false)
			},
		},
		{
			name: "delete", operation: interfaces.OPERATION_TYPE_DELETE,
			invoke: func(service *conceptGroupService, ctx context.Context) error {
				return service.DeleteConceptGroupByID(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, "cg-1")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cga := bmock.NewMockConceptGroupAccess(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)
			denied := errors.New("denied")
			if tt.name == "detail" {
				cga.EXPECT().GetConceptGroupByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "cg-1").
					Return(&interfaces.ConceptGroup{CGID: "cg-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}, nil)
			} else {
				cga.EXPECT().CheckConceptGroupExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "cg-1").
					Return("group", true, nil)
			}
			if tt.name == "detail" {
				ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
					[]string{"kn-1/cg-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).Return(nil, denied)
			} else {
				ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
					Type: interfaces.RESOURCE_TYPE_CONCEPT_GROUP, ID: "kn-1/cg-1",
				}, []string{tt.operation}).Return(denied)
			}
			service := &conceptGroupService{cga: cga, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}

func TestConceptGroupMembershipWritesRequireCanonicalGroupModify(t *testing.T) {
	for _, strictMode := range []bool{true, false} {
		t.Run(map[bool]string{true: "strict", false: "non-strict"}[strictMode], func(t *testing.T) {
			for _, operation := range []struct {
				name   string
				invoke func(*conceptGroupService, context.Context) error
			}{
				{
					name: "add",
					invoke: func(service *conceptGroupService, ctx context.Context) error {
						_, err := service.AddObjectTypesToConceptGroup(ctx, nil, "kn-1", interfaces.MAIN_BRANCH,
							"cg-1", []interfaces.ID{{ID: "ot-1"}}, interfaces.ImportMode_Normal, strictMode)
						return err
					},
				},
				{
					name: "delete",
					invoke: func(service *conceptGroupService, ctx context.Context) error {
						return service.DeleteObjectTypesFromGroup(ctx, nil, "kn-1", interfaces.MAIN_BRANCH,
							"cg-1", []string{"ot-1"})
					},
				},
			} {
				t.Run(operation.name, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					ps := bmock.NewMockPermissionService(ctrl)
					denied := errors.New("denied")
					ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
						Type: interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
						ID:   "kn-1/cg-1",
					}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(denied)

					service := &conceptGroupService{ps: ps}
					if err := operation.invoke(service, context.Background()); !errors.Is(err, denied) {
						t.Fatalf("operation error = %v, want %v", err, denied)
					}
				})
			}
		})
	}
}

func TestConceptGroupRelationReadRequiresCanonicalGroupView(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	denied := errors.New("denied")
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		ID:   "kn-1/cg-1",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(denied)

	service := &conceptGroupService{ps: ps}
	_, err := service.ListConceptGroupRelations(context.Background(), interfaces.ConceptGroupRelationsQueryParams{
		KNID:  "kn-1",
		CGIDs: []string{"cg-1"},
	})
	if !errors.Is(err, denied) {
		t.Fatalf("ListConceptGroupRelations() error = %v, want %v", err, denied)
	}
}

func TestPublicConceptGroupValidationRequiresParentKNModify(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	denied := errors.New("denied")
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   "kn-1",
	}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(denied)

	service := &conceptGroupService{ps: ps}
	err := service.ValidateConceptGroups(context.Background(), "kn-1", interfaces.MAIN_BRANCH,
		[]*interfaces.ConceptGroup{{CGID: "cg-1"}}, true, nil, interfaces.ImportMode_Normal)
	if !errors.Is(err, denied) {
		t.Fatalf("ValidateConceptGroups() error = %v, want %v", err, denied)
	}
}
