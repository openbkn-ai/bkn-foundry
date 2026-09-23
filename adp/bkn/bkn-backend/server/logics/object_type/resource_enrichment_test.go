// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func resourceSearchEntry(otID, resourceID string) map[string]any {
	return map[string]any{
		"id":     otID,
		"name":   otID,
		"kn_id":  "kn1",
		"branch": interfaces.MAIN_BRANCH,
		"_score": 1.0,
		"data_source": map[string]any{
			"type": interfaces.DATA_SOURCE_TYPE_RESOURCE,
			"id":   resourceID,
		},
		"data_properties": []any{
			map[string]any{
				"name":         "code",
				"type":         "string",
				"mapped_field": map[string]any{"name": "code"},
			},
		},
	}
}

func vegaResource(id string) *interfaces.VegaResource {
	return &interfaces.VegaResource{
		ID:   id,
		Name: id + " name",
		SchemaDefinition: []*interfaces.Property{
			{Name: "code", DisplayName: id + " code", Type: "string"},
		},
	}
}

func newResourceSearchService(t *testing.T, visible []string, entries []map[string]any) (*objectTypeService, *bmock.MockVegaBackendService) {
	ctrl := gomock.NewController(t)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	ota.EXPECT().GetObjectTypeIDsByKnID(gomock.Any(), gomock.Any(), gomock.Any()).Return(visible, nil).AnyTimes()
	ps.EXPECT().FilterVisibleResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(allowAllPermissionResources).AnyTimes()
	vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).
		Return(&interfaces.DatasetQueryResponse{Entries: entries}, nil)
	return &objectTypeService{
		appSetting: &common.AppSetting{},
		vbs:        vbs,
		ps:         ps,
		ota:        ota,
	}, vbs
}

func searchQuery() *interfaces.ConceptsQuery {
	return &interfaces.ConceptsQuery{KNID: "kn1", Branch: interfaces.MAIN_BRANCH, Limit: 10}
}

func assertEnrichedFrom(t *testing.T, objectType *interfaces.ObjectType, resourceID string) {
	t.Helper()
	if objectType.DataSource.Name != resourceID+" name" {
		t.Fatalf("%s data source name = %q, want it from %s", objectType.OTID, objectType.DataSource.Name, resourceID)
	}
	if got := objectType.DataProperties[0].MappedField.DisplayName; got != resourceID+" code" {
		t.Fatalf("%s mapped field display name = %q", objectType.OTID, got)
	}
	if len(objectType.DataProperties[0].ConditionOperations) == 0 {
		t.Fatalf("%s condition operations were not derived", objectType.OTID)
	}
}

func assertNotEnriched(t *testing.T, objectType *interfaces.ObjectType) {
	t.Helper()
	if objectType.DataSource.Name != "" || objectType.DataProperties[0].ConditionOperations != nil {
		t.Fatalf("%s was enriched without a resource: %#v", objectType.OTID, objectType.DataSource)
	}
}

func TestSearchObjectTypesReadsResourcesInOneBatch(t *testing.T) {
	service, vbs := newResourceSearchService(t, []string{"ot1", "ot2", "ot3"}, []map[string]any{
		resourceSearchEntry("ot1", "r1"),
		resourceSearchEntry("ot2", "r2"),
		resourceSearchEntry("ot3", "r1"),
	})
	// One read for all hits, each resource asked for once; no single reads at all.
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"r1", "r2"}).
		Return([]*interfaces.VegaResource{vegaResource("r2"), vegaResource("r1")}, nil).Times(1)

	resp, err := service.SearchObjectTypes(context.Background(), searchQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(resp.Entries))
	}
	for i, want := range []struct{ otID, resourceID string }{{"ot1", "r1"}, {"ot2", "r2"}, {"ot3", "r1"}} {
		objectType := resp.Entries[i]
		if objectType.OTID != want.otID {
			t.Fatalf("entry %d = %s, want %s", i, objectType.OTID, want.otID)
		}
		assertEnrichedFrom(t, objectType, want.resourceID)
		if objectType.Score == nil || *objectType.Score != 1.0 {
			t.Fatalf("%s score = %v", objectType.OTID, objectType.Score)
		}
	}
}

func TestSearchObjectTypesFallsBackToSingleReadsWhenBatchIsRefused(t *testing.T) {
	service, vbs := newResourceSearchService(t, []string{"ot1", "ot2"}, []map[string]any{
		resourceSearchEntry("ot1", "r1"),
		resourceSearchEntry("ot2", "r2"),
	})
	// Vega refuses the whole batch when one resource is not viewable; the rest must still enrich.
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"r1", "r2"}).Return(nil, errors.New("GetResourcesByIDs returned HTTP 403"))
	vbs.EXPECT().GetResourceByID(gomock.Any(), "r1").Return(nil, errors.New("GetResourceByID returned HTTP 403"))
	vbs.EXPECT().GetResourceByID(gomock.Any(), "r2").Return(vegaResource("r2"), nil)

	resp, err := service.SearchObjectTypes(context.Background(), searchQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(resp.Entries))
	}
	assertNotEnriched(t, resp.Entries[0])
	assertEnrichedFrom(t, resp.Entries[1], "r2")
}

func TestSearchObjectTypesLeavesMissingResourceUnenriched(t *testing.T) {
	service, vbs := newResourceSearchService(t, []string{"ot1", "ot2"}, []map[string]any{
		resourceSearchEntry("ot1", "gone"),
		resourceSearchEntry("ot2", "r2"),
	})
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"gone", "r2"}).
		Return([]*interfaces.VegaResource{vegaResource("r2")}, nil)

	resp, err := service.SearchObjectTypes(context.Background(), searchQuery())
	if err != nil {
		t.Fatal(err)
	}
	assertNotEnriched(t, resp.Entries[0])
	assertEnrichedFrom(t, resp.Entries[1], "r2")
}

func TestFetchObjectTypeResourcesSplitsLargeBatches(t *testing.T) {
	ctrl := gomock.NewController(t)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	service := &objectTypeService{vbs: vbs}

	total := vegaResourceBatchSize*2 + 7
	var batchSizes []int
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, ids []string) ([]*interfaces.VegaResource, error) {
			batchSizes = append(batchSizes, len(ids))
			out := make([]*interfaces.VegaResource, 0, len(ids))
			for _, id := range ids {
				out = append(out, vegaResource(id))
			}
			return out, nil
		}).Times(3)

	objectTypes := make([]*interfaces.ObjectType, 0, total)
	for i := 0; i < total; i++ {
		objectTypes = append(objectTypes, &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: fmt.Sprintf("r%d", i)},
		}})
	}
	resources := service.fetchObjectTypeResources(context.Background(), objectTypes)
	if len(resources) != total {
		t.Fatalf("resources = %d, want %d", len(resources), total)
	}
	if fmt.Sprint(batchSizes) != fmt.Sprint([]int{vegaResourceBatchSize, vegaResourceBatchSize, 7}) {
		t.Fatalf("batch sizes = %v", batchSizes)
	}
}

func TestFetchObjectTypeResourcesSkipsUnboundObjectTypes(t *testing.T) {
	ctrl := gomock.NewController(t)
	vbs := bmock.NewMockVegaBackendService(ctrl) // any Vega call fails the test
	service := &objectTypeService{vbs: vbs}

	resources := service.fetchObjectTypeResources(context.Background(), []*interfaces.ObjectType{
		nil,
		{},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE}}},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{DataSource: &interfaces.ResourceInfo{Type: "data_view", ID: "dv1"}}},
	})
	if len(resources) != 0 {
		t.Fatalf("resources = %v, want none", resources)
	}
}

func TestProcessObjectTypeDetailsProjectsVegaIndexStatusAndConfiguredFeatures(t *testing.T) {
	service := &objectTypeService{appSetting: &common.AppSetting{}}
	objectType := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:       "ot1",
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "r1"},
			DataProperties: []*interfaces.DataProperty{
				{Name: "summary", MappedField: &interfaces.Field{Name: "summary"}},
				{Name: "embedding", MappedField: &interfaces.Field{Name: "embedding"}},
			},
		},
		Status: &interfaces.ObjectTypeStatus{IndexAvailable: false},
	}
	resource := &interfaces.VegaResource{
		ID:               "r1",
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		SchemaDefinition: []*interfaces.Property{
			{Name: "summary", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.FieldFeatureType_Keyword}}},
			{Name: "fulltext_summary", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.FieldFeatureType_Fulltext, RefProperty: "summary"}}},
			{Name: "embedding", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.FieldFeatureType_Vector}}},
		},
	}

	if err := service.processObjectTypeDetails(context.Background(), objectType, map[string]vegaResourceLookup{
		"r1": {resource: resource},
	}); err != nil {
		t.Fatal(err)
	}
	if objectType.IndexStatus == nil || objectType.IndexStatus.State != interfaces.ObjectTypeIndexStateAvailable {
		t.Fatalf("index status = %#v, want available", objectType.IndexStatus)
	}
	if !objectType.Status.IndexAvailable {
		t.Fatal("compatibility index_available was not derived from the current Vega status")
	}
	summaryFeatures := objectType.DataProperties[0].IndexFeatures
	if len(summaryFeatures) != 2 || summaryFeatures[0].Type != interfaces.FieldFeatureType_Keyword ||
		summaryFeatures[1].Type != interfaces.FieldFeatureType_Fulltext || !*summaryFeatures[0].Available || !*summaryFeatures[1].Available {
		t.Fatalf("summary features = %#v", summaryFeatures)
	}
	embeddingFeatures := objectType.DataProperties[1].IndexFeatures
	if len(embeddingFeatures) != 1 || embeddingFeatures[0].Type != interfaces.FieldFeatureType_Vector || !*embeddingFeatures[0].Available {
		t.Fatalf("embedding features = %#v", embeddingFeatures)
	}
}

func TestProcessObjectTypeDetailsKeepsConfiguredFeaturesWhenIndexIsUnavailable(t *testing.T) {
	service := &objectTypeService{appSetting: &common.AppSetting{}}
	objectType := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "r1"},
		DataProperties: []*interfaces.DataProperty{{Name: "summary", MappedField: &interfaces.Field{Name: "summary"}}},
	}}
	resource := &interfaces.VegaResource{
		ID: "r1", LocalIndexStatus: "stale",
		SchemaDefinition: []*interfaces.Property{{Name: "summary", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.FieldFeatureType_Fulltext}}}},
	}

	if err := service.processObjectTypeDetails(context.Background(), objectType, map[string]vegaResourceLookup{
		"r1": {resource: resource},
	}); err != nil {
		t.Fatal(err)
	}
	if objectType.IndexStatus == nil || objectType.IndexStatus.State != interfaces.ObjectTypeIndexStateUnavailable {
		t.Fatalf("index status = %#v, want unavailable", objectType.IndexStatus)
	}
	features := objectType.DataProperties[0].IndexFeatures
	if len(features) != 1 || features[0].Available == nil || *features[0].Available {
		t.Fatalf("unavailable feature should remain configured and false, got %#v", features)
	}
}

func TestProcessObjectTypeDetailsDoesNotTurnVegaFailureIntoUnavailable(t *testing.T) {
	service := &objectTypeService{appSetting: &common.AppSetting{}}
	objectType := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "r1"},
	}}

	if err := service.processObjectTypeDetails(context.Background(), objectType, map[string]vegaResourceLookup{
		"r1": {err: errors.New("vega timed out")},
	}); err != nil {
		t.Fatal(err)
	}
	if objectType.IndexStatus == nil || objectType.IndexStatus.State != interfaces.ObjectTypeIndexStateUnknown {
		t.Fatalf("index status = %#v, want unknown", objectType.IndexStatus)
	}
}
