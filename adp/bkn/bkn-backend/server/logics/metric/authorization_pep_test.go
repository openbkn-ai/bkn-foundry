// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package metric

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestMetricSingleResourcePEP(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		invoke    func(*metricService, context.Context) error
	}{
		{"detail", interfaces.OPERATION_TYPE_VIEW_DETAIL, func(service *metricService, ctx context.Context) error {
			_, err := service.GetMetricByID(ctx, "kn-1", interfaces.MAIN_BRANCH, "metric-1")
			return err
		}},
		{"update", interfaces.OPERATION_TYPE_MODIFY, func(service *metricService, ctx context.Context) error {
			return service.UpdateMetric(ctx, nil, &interfaces.MetricDefinition{
				ID: "metric-1", KnID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			}, false)
		}},
		{"delete", interfaces.OPERATION_TYPE_DELETE, func(service *metricService, ctx context.Context) error {
			return service.DeleteMetricsByIDs(ctx, nil, "kn-1", interfaces.MAIN_BRANCH, []string{"metric-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ma := bmock.NewMockMetricAccess(ctrl)
			ps := bmock.NewMockPermissionService(ctrl)
			denied := errors.New("denied")
			if tt.name == "detail" {
				ma.EXPECT().GetMetricByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").
					Return(&interfaces.MetricDefinition{ID: "metric-1", KnID: "kn-1", Branch: interfaces.MAIN_BRANCH}, nil)
			} else {
				ma.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").
					Return("metric", true, nil)
			}
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_METRIC, ID: "kn-1/metric-1",
			}, []string{tt.operation}).Return(denied)
			service := &metricService{ma: ma, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}
