// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package risk_type

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestRiskTypeSingleResourcePEP(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
	tests := []struct {
		name      string
		operation string
		invoke    func(*riskTypeService, context.Context) error
	}{
		{"detail", interfaces.OPERATION_TYPE_VIEW_DETAIL, func(service *riskTypeService, ctx context.Context) error {
			_, err := service.GetRiskTypesByIDs(ctx, "kn-1", interfaces.MAIN_BRANCH, []string{"risk-1"})
			return err
		}},
		{"update", interfaces.OPERATION_TYPE_MODIFY, func(service *riskTypeService, ctx context.Context) error {
			return service.UpdateRiskType(ctx, nil, &interfaces.RiskType{
				RTID: "risk-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			})
		}},
		{"delete", interfaces.OPERATION_TYPE_DELETE, func(service *riskTypeService, ctx context.Context) error {
			return service.DeleteRiskTypesByIDs(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, []string{"risk-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rta := bmock.NewMockRiskTypeAccess(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)
			denied := errors.New("denied")
			if tt.name == "detail" {
				rta.EXPECT().GetRiskTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"risk-1"}).
					Return([]*interfaces.RiskType{{RTID: "risk-1"}}, nil)
			} else {
				rta.EXPECT().CheckRiskTypeExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "risk-1").
					Return("risk", true, nil)
			}
			if tt.name == "detail" {
				ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_RISK_TYPE,
					[]string{"kn-1/risk-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).Return(nil, denied)
			} else {
				ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
					Type: interfaces.RESOURCE_TYPE_RISK_TYPE, ID: "kn-1/risk-1",
				}, []string{tt.operation}).Return(denied)
			}
			service := &riskTypeService{rta: rta, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}
