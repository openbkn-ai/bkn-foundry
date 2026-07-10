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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
	"vega-backend/logics/connectors"
)

func TestReconcileTableResources(t *testing.T) {
	t.Run("marks new table resource", func(t *testing.T) {
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

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
	})

	t.Run("refreshes missing status when already stale", func(t *testing.T) {
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

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("does not disable user-disabled resource", func(t *testing.T) {
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

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("marks restored stale table", func(t *testing.T) {
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

		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
		assert.Equal(t, 1, result.RestoredCount)
		assert.Zero(t, result.UnchangedCount)
	})
}

func TestUpdateDiscoverResultForEnrichStatus(t *testing.T) {
	t.Run("increments status counters", func(t *testing.T) {
		result := &interfaces.DiscoverResult{}

		updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusUnchanged)
		updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusUpdated)
		updateDiscoverResultForEnrichStatus(result, interfaces.DiscoverStatusError)

		assert.Equal(t, 1, result.UnchangedCount)
		assert.Equal(t, 1, result.UpdatedCount)
		assert.Equal(t, 1, result.FailedCount)
	})
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
	t.Run("continues when one table fails", func(t *testing.T) {
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
				assert.Equal(t, "r1", resource.ID)
				assert.Equal(t, interfaces.DiscoverStatusError, resource.LastDiscoverStatus)
				assert.NotEmpty(t, resource.StatusMessage)
				return nil
			})
		rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
			DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
				assert.Equal(t, "r2", resource.ID)
				require.Len(t, resource.SchemaDefinition, 1)
				assert.Equal(t, "id", resource.SchemaDefinition[0].Name)
				return nil
			})

		result := &interfaces.DiscoverResult{}
		err := dh.enrichTableMetadata(context.Background(), connector, []tableDiscoverItem{
			{resource: inaccessible, tableMeta: &interfaces.TableMeta{Name: "no_access", Schema: "public"}},
			{resource: accessible, tableMeta: &interfaces.TableMeta{Name: "erp_material", Schema: "public"}},
		}, result)

		require.NoError(t, err)
		assert.Equal(t, 1, result.FailedCount)
	})
}

func TestSourceSnapshotHashIgnoresDerivedAndUserEditableFields(t *testing.T) {
	t.Run("ignores derived and user editable fields", func(t *testing.T) {
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

		assert.Equal(t, before, sourceSnapshotHash(resource))
	})
}

func TestSourceSnapshotHashChangesForSourceMetadata(t *testing.T) {
	t.Run("changes for source metadata", func(t *testing.T) {
		resource := &interfaces.Resource{
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "int"}},
			SourceMetadata:   map[string]any{"original_name": "users", "columns": []interfaces.TableColumnMeta{{Name: "id", Type: "int"}}},
		}
		before := sourceSnapshotHash(resource)

		resource.SourceMetadata["columns"] = []interfaces.TableColumnMeta{{Name: "id", Type: "int"}, {Name: "name", Type: "varchar"}}

		assert.NotEqual(t, before, sourceSnapshotHash(resource))
	})
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
