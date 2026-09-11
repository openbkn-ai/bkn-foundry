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

	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func metricAuthorizationDefinition(scopeRef string) *interfaces.MetricDefinition {
	return &interfaces.MetricDefinition{
		ID: "metric-1", Name: "Metric 1", KnID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ScopeType: interfaces.ScopeTypeObjectType, ScopeRef: scopeRef,
		TimeDimension:      &interfaces.MetricTimeDimension{Property: "event_time"},
		AnalysisDimensions: []interfaces.MetricAnalysisDimension{{Name: "region"}},
		CalculationFormula: &interfaces.MetricCalculationFormula{
			Aggregation: interfaces.MetricAggregation{Property: "amount", Aggr: interfaces.MetricAggrSum},
		},
	}
}

func metricAuthorizationObjectType(id string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:       id,
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
			DataProperties: []*interfaces.DataProperty{
				{Name: "amount"}, {Name: "event_time"}, {Name: "region"},
			},
		},
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	}
}

func TestMetricCreateRequiresObjectTypeAndPropertyAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dbMock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	definition := metricAuthorizationDefinition("orders")
	ma.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").Return("", false, nil)
	ma.EXPECT().CheckMetricExistByName(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "Metric 1").Return("", false, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN, ID: "kn-1",
	}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
	ots.EXPECT().GetObjectTypeByID(gomock.Any(), tx, "kn-1", interfaces.MAIN_BRANCH, "orders").
		Return(metricAuthorizationObjectType("orders"), nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ID: "kn-1/orders",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA}).Return(nil)
	ps.EXPECT().RequireFullPropertyAccess(gomock.Any(), "kn-1/orders", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, properties []string) error {
			seen := map[string]bool{}
			for _, property := range properties {
				seen[property] = true
			}
			for _, want := range []string{"amount", "event_time", "region"} {
				if !seen[want] {
					t.Fatalf("property authorization omitted %q from %v", want, properties)
				}
			}
			return nil
		})
	ma.EXPECT().CreateMetric(gomock.Any(), tx, definition).Return(nil)
	ps.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		interfaces.RESOURCE_TYPE_KN, gomock.Any()).Return(nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil)

	service := &metricService{appSetting: &common.AppSetting{}, ma: ma, ps: ps, ots: ots, vbs: vbs}
	if _, err := service.CreateMetrics(context.Background(), tx, []*interfaces.MetricDefinition{definition}, false,
		interfaces.ImportMode_Normal); err != nil {
		t.Fatalf("CreateMetrics() error = %v", err)
	}
}

func TestMetricMetadataUpdateDoesNotReauthorizeObjectType(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dbMock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	previous := metricAuthorizationDefinition("orders")
	updated := metricAuthorizationDefinition("orders")
	updated.Name = "Renamed Metric"
	ma.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").Return("Metric 1", true, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_METRIC, ID: "kn-1/metric-1",
	}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
	ma.EXPECT().GetMetricByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").Return(previous, nil)
	ma.EXPECT().UpdateMetric(gomock.Any(), tx, updated).Return(nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil)

	service := &metricService{appSetting: &common.AppSetting{}, ma: ma, ps: ps, vbs: vbs}
	if err := service.UpdateMetric(context.Background(), tx, updated, false); err != nil {
		t.Fatalf("UpdateMetric() error = %v", err)
	}
}

func TestMetricDependencyUpdateReauthorizesNewObjectTypeBeforeWrite(t *testing.T) {
	ctrl := gomock.NewController(t)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	previous := metricAuthorizationDefinition("orders")
	updated := metricAuthorizationDefinition("customers")
	denied := errors.New("denied")
	ma.EXPECT().CheckMetricExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").Return("Metric 1", true, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_METRIC, ID: "kn-1/metric-1",
	}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
	ma.EXPECT().GetMetricByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "metric-1").Return(previous, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ID: "kn-1/customers",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA}).Return(denied)

	service := &metricService{ma: ma, ps: ps, ots: ots}
	if err := service.UpdateMetric(context.Background(), nil, updated, false); !errors.Is(err, denied) {
		t.Fatalf("UpdateMetric() error = %v, want %v", err, denied)
	}
}

func TestMetricDetailExposesOnlyReferencedPropertyMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	definition := metricAuthorizationDefinition("orders")
	objectType := metricAuthorizationObjectType("orders")
	objectType.OTName = "Orders"
	objectType.DataProperties = append(objectType.DataProperties, &interfaces.DataProperty{
		Name: "secret", DisplayName: "Secret", Type: "string",
	})
	ots.EXPECT().GetObjectTypeByID(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH, "orders").
		Return(objectType, nil)
	ma.EXPECT().GetMetricsByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"metric-1"}).
		Return([]*interfaces.MetricDefinition{definition}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/metric-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/metric-1": {ResourceID: "kn-1/metric-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)

	service := &metricService{ma: ma, ps: ps, ots: ots}
	metrics, err := service.GetMetricsByIDs(context.Background(), "kn-1", interfaces.MAIN_BRANCH, []string{"metric-1"})
	if err != nil {
		t.Fatalf("GetMetricsByIDs() error = %v", err)
	}
	definition = metrics[0]
	if definition.ScopeName != "Orders" {
		t.Fatalf("ScopeName = %q", definition.ScopeName)
	}
	seen := map[string]bool{}
	for _, property := range definition.DependencyProperties {
		seen[property.Name] = true
	}
	if seen["secret"] || !seen["amount"] || !seen["event_time"] || !seen["region"] {
		t.Fatalf("DependencyProperties = %#v", definition.DependencyProperties)
	}
}

func TestMetricDetailRemainsReadableWhenScopeObjectTypeIsMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	definition := metricAuthorizationDefinition("deleted-orders")
	ots.EXPECT().GetObjectTypeByID(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH, "deleted-orders").
		Return(nil, rest.NewHTTPError(context.Background(), http.StatusNotFound,
			berrors.BknBackend_ObjectType_ObjectTypeNotFound))
	ma.EXPECT().GetMetricsByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"metric-1"}).
		Return([]*interfaces.MetricDefinition{definition}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
		[]string{"kn-1/metric-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
		gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{
		"kn-1/metric-1": {ResourceID: "kn-1/metric-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}, nil)

	service := &metricService{ma: ma, ps: ps, ots: ots}
	metrics, err := service.GetMetricsByIDs(context.Background(), "kn-1", interfaces.MAIN_BRANCH, []string{"metric-1"})
	if err != nil {
		t.Fatalf("GetMetricsByIDs() error = %v", err)
	}
	if len(metrics) != 1 || metrics[0] != definition {
		t.Fatalf("GetMetricsByIDs() = %#v", metrics)
	}
	if definition.ScopeName != "" || len(definition.DependencyProperties) != 0 {
		t.Fatalf("unexpected dependency metadata: %#v", definition)
	}
}

func TestCollectMetricConditionFieldsFallsBackToAllForInvalidMultiMatchFields(t *testing.T) {
	propertyNames := []string{"amount", "region"}
	for _, fields := range []any{
		map[string]any{"unexpected": true},
		[]any{"amount", 1},
		[]string{},
		[]string{" "},
	} {
		condition := &cond.CondCfg{
			Operation: cond.OperationMultiMatch,
			RemainCfg: map[string]any{"fields": fields},
		}
		actual := collectMetricConditionFields(condition, propertyNames)
		seen := make(map[string]bool, len(actual))
		for _, property := range actual {
			seen[property] = true
		}
		if len(seen) != len(propertyNames) || !seen["amount"] || !seen["region"] {
			t.Fatalf("collectMetricConditionFields(%#v) = %v, want all properties", fields, actual)
		}
	}
}

func TestMetricDependencyPropertyCandidatesIncludeOnlyFullAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	objectType := metricAuthorizationObjectType("orders")
	objectType.DataProperties = append(objectType.DataProperties, &interfaces.DataProperty{Name: "secret"})
	ots.EXPECT().GetObjectTypeByID(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH, "orders").
		Return(objectType, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ID: "kn-1/orders",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA}).Return(nil)
	ps.EXPECT().FilterFullPropertyAccess(gomock.Any(), "kn-1/orders",
		[]string{"amount", "event_time", "region", "secret"}).Return([]string{"amount", "region"}, nil)

	service := &metricService{ps: ps, ots: ots}
	properties, err := service.GetMetricDependencyProperties(context.Background(), "kn-1",
		interfaces.MAIN_BRANCH, "orders")
	if err != nil {
		t.Fatalf("GetMetricDependencyProperties() error = %v", err)
	}
	if len(properties) != 2 || properties[0].Name != "amount" || properties[1].Name != "region" {
		t.Fatalf("GetMetricDependencyProperties() = %#v", properties)
	}
}

func TestMetricSingleResourceAuthorization(t *testing.T) {
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
			if tt.name == "detail" {
				ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC,
					[]string{"kn-1/metric-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).Return(nil, denied)
			} else {
				ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
					Type: interfaces.RESOURCE_TYPE_METRIC, ID: "kn-1/metric-1",
				}, []string{tt.operation}).Return(denied)
			}
			service := &metricService{ma: ma, ps: ps}
			if err := tt.invoke(service, context.Background()); !errors.Is(err, denied) {
				t.Fatalf("operation error = %v, want %v", err, denied)
			}
		})
	}
}

func TestMetricListAuthorizationFiltersBeforeTotalAndPagination(t *testing.T) {
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
		[]string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_QUERY_DATA,
			interfaces.OPERATION_TYPE_MODIFY,
			interfaces.OPERATION_TYPE_DELETE,
		}).Return(map[string]interfaces.PermissionResourceOps{
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

func TestMetricBatchDeleteAuthorizationRejectsBeforeBusinessWrites(t *testing.T) {
	ctrl := gomock.NewController(t)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ma.EXPECT().GetMetricsByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"metric-1", "metric-2"}).Return([]*interfaces.MetricDefinition{
		{ID: "metric-1"}, {ID: "metric-2"},
	}, nil)
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

func TestMetricBatchOverwriteAuthorizationRejectsAndRollsBackBeforeBusinessWrites(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = db.Close() }()
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
