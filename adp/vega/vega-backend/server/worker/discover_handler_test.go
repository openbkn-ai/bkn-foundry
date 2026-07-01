// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
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

	if result.UnchangedCount != 1 || result.UpdatedCount != 1 {
		t.Fatalf("expected unchanged=1 updated=1, got unchanged=%d updated=%d", result.UnchangedCount, result.UpdatedCount)
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
