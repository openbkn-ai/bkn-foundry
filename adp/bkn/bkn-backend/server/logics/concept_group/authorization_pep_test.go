// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package concept_group

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestConceptGroupSingleResourcePEP(t *testing.T) {
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
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_CONCEPT_GROUP, ID: "kn-1/cg-1",
			}, []string{tt.operation}).Return(denied)
			service := &conceptGroupService{cga: cga, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}
