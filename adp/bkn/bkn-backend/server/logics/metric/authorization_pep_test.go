// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package metric

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

func TestMetricSingleResourcePEP(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
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

func TestMetricListPEPFiltersBeforeTotalAndPagination(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
	ctrl := gomock.NewController(t)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	query := interfaces.MetricsListQueryParams{
		KNID: "kn-1",
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Offset: 1,
			Limit:  1,
		},
	}
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.AssignableToTypeOf(query)).DoAndReturn(
		func(_ context.Context, candidateQuery interfaces.MetricsListQueryParams) ([]*interfaces.MetricDefinition, error) {
			if candidateQuery.Offset != 0 || candidateQuery.Limit != -1 {
				t.Fatalf("candidate query offset/limit = %d/%d", candidateQuery.Offset, candidateQuery.Limit)
			}
			return []*interfaces.MetricDefinition{
				{ID: "metric-1", KnID: "kn-1"},
				{ID: "metric-2", KnID: "kn-1"},
				{ID: "metric-3", KnID: "kn-1"},
			}, nil
		})
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/metric-1", "kn-1/metric-2", "kn-1/metric-3"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/metric-1": {ResourceID: "kn-1/metric-1"},
		"kn-1/metric-3": {ResourceID: "kn-1/metric-3"},
	}, nil)

	result, err := (&metricService{ma: ma, ps: ps}).ListMetrics(context.Background(), query)
	if err != nil {
		t.Fatalf("ListMetrics() error = %v", err)
	}
	if result.TotalCount != 2 || len(result.Entries) != 1 || result.Entries[0].ID != "metric-3" {
		t.Fatalf("ListMetrics() = %#v", result)
	}
}

func TestMetricBatchDeletePEPRejectsBeforeBusinessWrites(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
	ctrl := gomock.NewController(t)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	for _, metricID := range []string{"metric-1", "metric-2"} {
		ma.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, metricID).
			Return(metricID, true, nil)
	}
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/metric-1", "kn-1/metric-2"}, []string{interfaces.OPERATION_TYPE_DELETE}, true,
		[]string{interfaces.OPERATION_TYPE_DELETE}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/metric-1": {ResourceID: "kn-1/metric-1", Operations: []string{interfaces.OPERATION_TYPE_DELETE}},
	}, nil)

	service := &metricService{ma: ma, ps: ps}
	err := service.DeleteMetricsByIDs(context.Background(), nil, "kn-1", interfaces.MAIN_BRANCH,
		[]string{"metric-1", "metric-2"})
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("DeleteMetricsByIDs() error = %v, want HTTP 403", err)
	}
}

func TestMetricBatchOverwritePEPRejectsAndRollsBackBeforeBusinessWrites(t *testing.T) {
	t.Setenv("KN_CHILD_RESOURCE_PEP_ENABLED", "true")
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	entries := []*interfaces.MetricDefinition{
		{ID: "metric-1", Name: "Metric 1", KnID: "kn-1", Branch: interfaces.MAIN_BRANCH},
		{ID: "metric-2", Name: "Metric 2", KnID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}

	dbMock.ExpectBegin()
	for _, entry := range entries {
		ma.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, entry.ID).
			Return(entry.Name, true, nil)
		ma.EXPECT().CheckMetricExistByName(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, entry.Name).
			Return(entry.ID, true, nil)
	}
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/metric-1", "kn-1/metric-2"}, []string{interfaces.OPERATION_TYPE_MODIFY}, true,
		[]string{interfaces.OPERATION_TYPE_MODIFY}).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/metric-1": {ResourceID: "kn-1/metric-1", Operations: []string{interfaces.OPERATION_TYPE_MODIFY}},
	}, nil)
	dbMock.ExpectRollback()

	service := &metricService{db: db, ma: ma, ps: ps}
	_, err = service.CreateMetrics(context.Background(), nil, entries, false, interfaces.ImportMode_Overwrite)
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("CreateMetrics() error = %v, want HTTP 403", err)
	}
	if err := dbMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations were not met: %v", err)
	}
}
