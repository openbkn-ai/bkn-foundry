// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics/connectors"
)

func TestReconcileTableResourcesMarksNew(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverHandler{rs: rs}

	table := &interfaces.TableMeta{Name: "users"}
	created := &interfaces.Resource{ID: "r1", SourceIdentifier: "users", Status: interfaces.ResourceStatusActive}
	rs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(created, nil)
	rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

	result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"},
		[]*interfaces.TableMeta{table}, nil, &actions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NewCount != 1 {
		t.Fatalf("expected 1 new resource, got %d", result.NewCount)
	}
	if len(items) != 1 || items[0].markAfterEnrich {
		t.Fatalf("new resources should keep last discover status as new during this scan")
	}
}

func TestReconcileTableResourcesRefreshesMissingWhenAlreadyStale(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverHandler{rs: rs}

	rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

	result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"}, nil,
		[]*interfaces.Resource{{
			ID:               "r1",
			SourceIdentifier: "users",
			Category:         interfaces.ResourceCategoryTable,
			Status:           interfaces.ResourceStatusStale,
		}}, &actions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StaleCount != 0 {
		t.Fatalf("already stale resource should not count as newly stale, got %d", result.StaleCount)
	}
}

func TestReconcileTableResourcesDoesNotDisableUserDisabledResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverHandler{rs: rs}

	rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

	result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"}, nil,
		[]*interfaces.Resource{{
			ID:               "r1",
			SourceIdentifier: "users",
			Category:         interfaces.ResourceCategoryTable,
			Status:           interfaces.ResourceStatusDisabled,
		}}, &actions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StaleCount != 0 {
		t.Fatalf("disabled resource should not move to stale, got stale count %d", result.StaleCount)
	}
}

func TestReconcileTableResourcesMarksRestored(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverHandler{rs: rs}

	rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusActive, "").Return(nil)
	rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusRestored).Return(nil)
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

	result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.Catalog{ID: "cat1"},
		[]*interfaces.TableMeta{{Name: "users"}},
		[]*interfaces.Resource{{
			ID:               "r1",
			SourceIdentifier: "users",
			Category:         interfaces.ResourceCategoryTable,
			Status:           interfaces.ResourceStatusStale,
		}}, &actions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].markAfterEnrich {
		t.Fatalf("restored resource should keep restored status during this scan")
	}
	if result.RestoredCount != 1 || result.UnchangedCount != 0 {
		t.Fatalf("expected restored=1 unchanged=0, got restored=%d unchanged=%d", result.RestoredCount, result.UnchangedCount)
	}
}

func TestUpdateDiscoverResultForEnrichStatus(t *testing.T) {
	result := &interfaces.DiscoverResult{}

	updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusUnchanged)
	updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusUpdated)
	updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusError)

	if result.UnchangedCount != 1 || result.UpdatedCount != 1 || result.FailedCount != 1 {
		t.Fatalf("expected unchanged=1 updated=1 failed=1, got unchanged=%d updated=%d failed=%d",
			result.UnchangedCount, result.UpdatedCount, result.FailedCount)
	}
}

func TestBuildSourceIdentifierUsesSchemaAsQueryableNamespace(t *testing.T) {
	dh := &DiscoverHandler{}

	cases := []struct {
		name  string
		table *interfaces.TableMeta
		want  string
	}{
		{
			name:  "postgresql schema table",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Schema: "public", Name: "supplier_catalog"},
			want:  "public.supplier_catalog",
		},
		{
			name:  "mariadb schema equals database",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Schema: "ecommerce_db", Name: "supplier_catalog"},
			want:  "ecommerce_db.supplier_catalog",
		},
		{
			name:  "database fallback",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Name: "supplier_catalog"},
			want:  "ecommerce_db.supplier_catalog",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := dh.buildSourceIdentifier(tt.table); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestEnrichTableMetadataContinuesWhenOneTableFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverHandler{rs: rs}

	inaccessible := &interfaces.Resource{ID: "r1", SourceIdentifier: "public.no_access", LastDiscoverStatus: interfaces.DiscoverStatusNew}
	accessible := &interfaces.Resource{
		ID:                 "r2",
		SourceIdentifier:   "public.erp_material",
		LastDiscoverStatus: interfaces.DiscoverStatusNew,
		SourceMetadata:     map[string]any{"original_name": "public.erp_material"},
	}
	connector := &fakeTableConnector{
		metaErrByName: map[string]error{
			"no_access": errors.New("permission denied"),
		},
		columnsByName: map[string][]interfaces.TableColumnMeta{
			"erp_material": {{Name: "id", Type: "int4"}},
		},
	}

	rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
		DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
			if resource.ID != "r1" {
				t.Fatalf("expected failed resource r1 to be updated first, got %s", resource.ID)
			}
			if resource.LastDiscoverStatus != interfaces.DiscoverStatusError {
				t.Fatalf("expected failed resource discover status error, got %s", resource.LastDiscoverStatus)
			}
			if resource.StatusMessage == "" {
				t.Fatalf("expected failed resource status message")
			}
			return nil
		})
	rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
		DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
			if resource.ID != "r2" {
				t.Fatalf("expected successful resource r2 to be updated, got %s", resource.ID)
			}
			if len(resource.SchemaDefinition) != 1 || resource.SchemaDefinition[0].Name != "id" {
				t.Fatalf("expected successful resource schema to be enriched")
			}
			return nil
		})

	result := &interfaces.DiscoverResult{}
	err := dh.enrichTableMetadata(context.Background(), connector, []tableDiscoverItem{
		{resource: inaccessible, tableMeta: &interfaces.TableMeta{Name: "no_access", Schema: "public"}},
		{resource: accessible, tableMeta: &interfaces.TableMeta{Name: "erp_material", Schema: "public"}},
	}, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FailedCount != 1 {
		t.Fatalf("expected failed count 1, got %d", result.FailedCount)
	}
}

func TestSourceSnapshotHashIgnoresDerivedAndUserEditableFields(t *testing.T) {
	resource := &interfaces.Resource{
		Description:      "user text",
		Tags:             []string{"a"},
		Name:             "users",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "int", Description: "derived"}},
		SourceMetadata:   map[string]any{"original_name": "users"},
	}
	before := sourceSnapshotHash(resource)

	resource.Description = "edited by user"
	resource.Tags = []string{"b"}
	resource.Name = "display name"
	resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "name", Type: "string"})

	if got := sourceSnapshotHash(resource); got != before {
		t.Fatalf("expected non-source-metadata fields to be ignored, got %s want %s", got, before)
	}
}

func TestSourceSnapshotHashChangesForSourceMetadata(t *testing.T) {
	resource := &interfaces.Resource{
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "int"}},
		SourceMetadata:   map[string]any{"original_name": "users", "columns": []interfaces.TableColumnMeta{{Name: "id", Type: "int"}}},
	}
	before := sourceSnapshotHash(resource)

	resource.SourceMetadata["columns"] = []interfaces.TableColumnMeta{{Name: "id", Type: "int"}, {Name: "name", Type: "varchar"}}

	if got := sourceSnapshotHash(resource); got == before {
		t.Fatalf("expected source snapshot hash to change when source metadata changes")
	}
}

type fakeTableConnector struct {
	metaErrByName map[string]error
	columnsByName map[string][]interfaces.TableColumnMeta
}

func (c *fakeTableConnector) GetType() string { return "fake" }

func (c *fakeTableConnector) GetName() string { return "fake" }

func (c *fakeTableConnector) GetMode() string { return "local" }

func (c *fakeTableConnector) GetCategory() string { return interfaces.ConnectorCategoryTable }

func (c *fakeTableConnector) GetEnabled() bool { return true }

func (c *fakeTableConnector) SetEnabled(bool) {}

func (c *fakeTableConnector) GetSensitiveFields() []string { return nil }

func (c *fakeTableConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return nil
}

func (c *fakeTableConnector) New(interfaces.ConnectorConfig) (connectors.Connector, error) {
	return c, nil
}

func (c *fakeTableConnector) Connect(context.Context) error { return nil }

func (c *fakeTableConnector) Ping(context.Context) error { return nil }

func (c *fakeTableConnector) Close(context.Context) error { return nil }

func (c *fakeTableConnector) TestConnection(context.Context) error { return nil }

func (c *fakeTableConnector) GetMetadata(context.Context) (map[string]any, error) {
	return nil, nil
}

func (c *fakeTableConnector) MapType(nativeType string) string {
	return nativeType
}

func (c *fakeTableConnector) ListTables(context.Context) ([]*interfaces.TableMeta, error) {
	return nil, nil
}

func (c *fakeTableConnector) GetTableMeta(_ context.Context, table *interfaces.TableMeta) error {
	if err := c.metaErrByName[table.Name]; err != nil {
		return err
	}
	table.Columns = c.columnsByName[table.Name]
	return nil
}

func (c *fakeTableConnector) ExecuteQuery(context.Context, *interfaces.Resource,
	*interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
	return nil, nil
}
