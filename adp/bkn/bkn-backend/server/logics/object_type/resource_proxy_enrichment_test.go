// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// Issue #1536: a caller who may view and query an object type but holds nothing on the Vega
// resource bound to it used to get the object type without condition_operations, which search
// then read as "no searchable field" and answered with an empty result.

var (
	readerAccount = interfaces.AccountInfo{ID: "reader-1", Type: interfaces.ACCESSOR_TYPE_USER}
	proxyAccount  = interfaces.AccountInfo{ID: "proxy-1", Type: interfaces.KNProxyAccountTypeApp}
)

func readerContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, readerAccount)
}

func forbidden(operation string) error {
	return &interfaces.VegaStatusError{Operation: operation, HTTPStatus: http.StatusForbidden}
}

func readyProxy() *interfaces.KNProxyAccount {
	return &interfaces.KNProxyAccount{
		KNID: "kn1", ProxyAccountID: proxyAccount.ID, ProxyAccountType: proxyAccount.Type, Version: 3,
		LifecycleStatus: interfaces.KNProxyLifecycleActive, SyncStatus: interfaces.KNProxySyncReady,
	}
}

func schemaBinding(objectTypeID, resourceID string) interfaces.KNProxyBinding {
	return interfaces.KNProxyBinding{
		ChildType: interfaces.MODULE_TYPE_OBJECT_TYPE, ChildID: objectTypeID,
		TargetType: "resource", TargetID: resourceID,
		Operation: interfaces.OPERATION_TYPE_VIEW_DETAIL,
	}
}

// indexedResource is a resource whose local index is built with a fulltext feature on "code",
// which is what makes the object type searchable by match.
func indexedResource(id string) *interfaces.VegaResource {
	res := vegaResource(id)
	res.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
	res.SchemaDefinition[0].Features = []interfaces.PropertyFeature{{FeatureType: interfaces.FieldFeatureType_Fulltext}}
	return res
}

func boundObjectType(otID, resourceID, branch string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: otID, OTName: otID,
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: resourceID},
			DataProperties: []*interfaces.DataProperty{{
				Name: "code", Type: "string", MappedField: &interfaces.Field{Name: "code"},
			}},
		},
		KNID: "kn1", Branch: branch,
	}
}

func hasOperation(objectType *interfaces.ObjectType, operation string) bool {
	for _, op := range objectType.DataProperties[0].ConditionOperations {
		if op == operation {
			return true
		}
	}
	return false
}

func newProxyEnrichmentService(t *testing.T) (*objectTypeService, *bmock.MockVegaBackendService, *bmock.MockKNProxyBindingResolver) {
	ctrl := gomock.NewController(t)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	resolver := bmock.NewMockKNProxyBindingResolver(ctrl)
	return &objectTypeService{appSetting: &common.AppSetting{}, vbs: vbs, kpr: resolver}, vbs, resolver
}

// expectCallerRefused makes Vega refuse r1 to the caller and serve every other resource.
func expectCallerRefused(vbs *bmock.MockVegaBackendService, batch []string) {
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), batch).Return(nil, forbidden("GetResourcesByIDs"))
	for _, id := range batch {
		if id == "r1" {
			vbs.EXPECT().GetResourceByID(gomock.Any(), id).Return(nil, forbidden("GetResourceByID"))
		} else {
			vbs.EXPECT().GetResourceByID(gomock.Any(), id).Return(indexedResource(id), nil)
		}
	}
}

func TestSearchObjectTypesReadsRefusedResourceAsTheProxyAccount(t *testing.T) {
	service, vbs := newResourceSearchService(t, []string{"ot1", "ot2"}, []map[string]any{
		resourceSearchEntry("ot1", "r1"),
		resourceSearchEntry("ot2", "r2"),
	})
	resolver := bmock.NewMockKNProxyBindingResolver(gomock.NewController(t))
	service.kpr = resolver
	expectCallerRefused(vbs, []string{"r1", "r2"})
	// Only the refused binding is resolved and re-read, and only as the proxy account; r2 stays a
	// caller read.
	resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", []interfaces.KNProxyBinding{schemaBinding("ot1", "r1")}).
		Return(readyProxy(), []interfaces.KNProxyBinding{schemaBinding("ot1", "r1")}, nil)
	vbs.EXPECT().GetResourcesByIDsAs(gomock.Any(), proxyAccount, []string{"r1"}).
		Return([]*interfaces.VegaResource{indexedResource("r1")}, nil)

	resp, err := service.SearchObjectTypes(readerContext(), searchQuery())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"r1", "r2"} {
		objectType := resp.Entries[i]
		assertEnrichedFrom(t, objectType, want)
		if !hasOperation(objectType, interfaces.DSL_TEXT_OPS[0]) {
			t.Fatalf("%s lost its index capability: %v", objectType.OTID, objectType.DataProperties[0].ConditionOperations)
		}
		encoded, _ := json.Marshal(objectType)
		if objectType.DataSourceMetadataUnavailable || strings.Contains(string(encoded), "data_source_metadata_unavailable") {
			t.Fatalf("%s marked unavailable after it was read: %s", objectType.OTID, encoded)
		}
	}
}

func TestEnrichmentUsesProxyOnlyForResolvedObjectTypes(t *testing.T) {
	service, vbs, resolver := newProxyEnrichmentService(t)
	published := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
	unpublished := boundObjectType("ot3", "r1", interfaces.MAIN_BRANCH)
	expectCallerRefused(vbs, []string{"r1"})
	// ot3 binds the same resource but is not a current published binding.
	resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1",
		[]interfaces.KNProxyBinding{schemaBinding("ot1", "r1"), schemaBinding("ot3", "r1")}).
		Return(readyProxy(), []interfaces.KNProxyBinding{schemaBinding("ot1", "r1")}, nil)
	vbs.EXPECT().GetResourcesByIDsAs(gomock.Any(), proxyAccount, []string{"r1"}).
		Return([]*interfaces.VegaResource{indexedResource("r1")}, nil)

	if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{published, unpublished}); err != nil {
		t.Fatal(err)
	}
	assertEnrichedFrom(t, published, "r1")
	assertNotEnriched(t, unpublished)
	if !unpublished.DataSourceMetadataUnavailable || published.DataSourceMetadataUnavailable {
		t.Fatalf("markers: published=%v unpublished=%v", published.DataSourceMetadataUnavailable, unpublished.DataSourceMetadataUnavailable)
	}
}

func TestEnrichmentReadsOnlyRequestedBindingsThroughProxy(t *testing.T) {
	service, vbs, resolver := newProxyEnrichmentService(t)
	objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
	expectCallerRefused(vbs, []string{"r1"})
	// A resolver answering with more than it was asked must not widen the proxy read.
	resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", gomock.Any()).
		Return(readyProxy(), []interfaces.KNProxyBinding{schemaBinding("ot1", "r1"), schemaBinding("ot9", "r9")}, nil)
	vbs.EXPECT().GetResourcesByIDsAs(gomock.Any(), proxyAccount, []string{"r1"}).
		Return([]*interfaces.VegaResource{indexedResource("r1")}, nil)

	if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{objectType}); err != nil {
		t.Fatal(err)
	}
	assertEnrichedFrom(t, objectType, "r1")
}

func TestEnrichmentKeepsRefusalWhenProxyCannotHelp(t *testing.T) {
	proxyError := func(status int, code string) error {
		return rest.NewHTTPError(context.Background(), status, code)
	}
	for _, tc := range []struct {
		name   string
		branch string
		setup  func(*bmock.MockVegaBackendService, *bmock.MockKNProxyBindingResolver)
	}{
		{
			name:   "proxy not synchronized",
			branch: interfaces.MAIN_BRANCH,
			setup: func(_ *bmock.MockVegaBackendService, resolver *bmock.MockKNProxyBindingResolver) {
				resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", gomock.Any()).
					Return(nil, nil, proxyError(http.StatusServiceUnavailable, berrors.BknBackend_KnowledgeNetwork_ProxySyncPending))
			},
		},
		{
			name:   "no proxy mapping",
			branch: interfaces.MAIN_BRANCH,
			setup: func(_ *bmock.MockVegaBackendService, resolver *bmock.MockKNProxyBindingResolver) {
				resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", gomock.Any()).
					Return(nil, nil, proxyError(http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_ProxyMappingNotFound))
			},
		},
		{
			name:   "binding not published",
			branch: interfaces.MAIN_BRANCH,
			setup: func(_ *bmock.MockVegaBackendService, resolver *bmock.MockKNProxyBindingResolver) {
				resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", gomock.Any()).
					Return(readyProxy(), []interfaces.KNProxyBinding{}, nil)
			},
		},
		{
			name:   "mapping without an application proxy account",
			branch: interfaces.MAIN_BRANCH,
			setup: func(_ *bmock.MockVegaBackendService, resolver *bmock.MockKNProxyBindingResolver) {
				mapping := readyProxy()
				mapping.ProxyAccountType = interfaces.ACCESSOR_TYPE_USER
				resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", gomock.Any()).
					Return(mapping, []interfaces.KNProxyBinding{schemaBinding("ot1", "r1")}, nil)
			},
		},
		{
			name:   "proxy read refused",
			branch: interfaces.MAIN_BRANCH,
			setup: func(vbs *bmock.MockVegaBackendService, resolver *bmock.MockKNProxyBindingResolver) {
				resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", gomock.Any()).
					Return(readyProxy(), []interfaces.KNProxyBinding{schemaBinding("ot1", "r1")}, nil)
				vbs.EXPECT().GetResourcesByIDsAs(gomock.Any(), proxyAccount, []string{"r1"}).
					Return(nil, forbidden("GetResourcesByIDsAs"))
			},
		},
		{
			// The proxy holds grants for the published model only; an editing branch is not covered.
			name:   "editing branch",
			branch: "draft",
			setup:  func(*bmock.MockVegaBackendService, *bmock.MockKNProxyBindingResolver) {},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, vbs, resolver := newProxyEnrichmentService(t)
			objectType := boundObjectType("ot1", "r1", tc.branch)
			expectCallerRefused(vbs, []string{"r1"})
			tc.setup(vbs, resolver)

			if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{objectType}); err != nil {
				t.Fatalf("enrichment failed the request: %v", err)
			}
			assertNotEnriched(t, objectType)
			if !objectType.DataSourceMetadataUnavailable {
				t.Fatal("an unread resource must be reported, not left to look unsearchable")
			}
		})
	}
}

func TestEnrichmentUsesProxyOnlyAfterARefusal(t *testing.T) {
	// The resolver mock has no expectation: reaching it fails the test.
	service, vbs, _ := newProxyEnrichmentService(t)
	objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"r1"}).Return(nil, errors.New("vega dependency request failed"))
	vbs.EXPECT().GetResourceByID(gomock.Any(), "r1").
		Return(nil, &interfaces.VegaStatusError{Operation: "GetResourceByID", HTTPStatus: http.StatusInternalServerError})

	if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{objectType}); err != nil {
		t.Fatal(err)
	}
	assertNotEnriched(t, objectType)
	if !objectType.DataSourceMetadataUnavailable {
		t.Fatal("failed read not reported")
	}
}

func TestEnrichmentNeedsTheCallerIdentityForTheProxy(t *testing.T) {
	service, vbs, _ := newProxyEnrichmentService(t)
	objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
	expectCallerRefused(vbs, []string{"r1"})

	if err := service.enrichObjectTypes(context.Background(), []*interfaces.ObjectType{objectType}); err != nil {
		t.Fatal(err)
	}
	assertNotEnriched(t, objectType)
}

func TestEnrichmentFullAccessNeverTouchesTheProxy(t *testing.T) {
	service, vbs, _ := newProxyEnrichmentService(t)
	objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
	// A stored document cannot make a readable resource look unavailable.
	objectType.DataSourceMetadataUnavailable = true
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"r1"}).Return([]*interfaces.VegaResource{indexedResource("r1")}, nil)

	if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{objectType}); err != nil {
		t.Fatal(err)
	}
	assertEnrichedFrom(t, objectType, "r1")
	if objectType.DataSourceMetadataUnavailable {
		t.Fatal("marker survived a successful read")
	}
}

// A resource that no longer exists is a known answer -- there is nothing to search -- so the
// object type is not marked as having unknown capabilities, whichever read found it missing.
func TestEnrichmentDoesNotMarkADeletedResourceUnavailable(t *testing.T) {
	t.Run("batch read leaves it out", func(t *testing.T) {
		service, vbs, _ := newProxyEnrichmentService(t)
		objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
		vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"r1"}).Return([]*interfaces.VegaResource{}, nil)

		if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{objectType}); err != nil {
			t.Fatal(err)
		}
		if objectType.DataSourceMetadataUnavailable {
			t.Fatal("a deleted resource was reported as unreadable")
		}
	})
	t.Run("single read answers not found", func(t *testing.T) {
		service, vbs, _ := newProxyEnrichmentService(t)
		objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
		vbs.EXPECT().GetResourcesByIDs(gomock.Any(), []string{"r1"}).Return(nil, errors.New("batch failed"))
		vbs.EXPECT().GetResourceByID(gomock.Any(), "r1").Return(nil, nil)

		if err := service.enrichObjectTypes(readerContext(), []*interfaces.ObjectType{objectType}); err != nil {
			t.Fatal(err)
		}
		if objectType.DataSourceMetadataUnavailable {
			t.Fatal("a deleted resource was reported as unreadable")
		}
	})
}

func TestEnrichmentSplitsLargeProxyReads(t *testing.T) {
	service, vbs, resolver := newProxyEnrichmentService(t)
	total := vegaResourceBatchSize + 3
	objectTypes := make([]*interfaces.ObjectType, 0, total)
	bindings := make([]interfaces.KNProxyBinding, 0, total)
	for i := 0; i < total; i++ {
		otID, resourceID := fmt.Sprintf("ot%d", i), fmt.Sprintf("r%d", i)
		objectTypes = append(objectTypes, boundObjectType(otID, resourceID, interfaces.MAIN_BRANCH))
		bindings = append(bindings, schemaBinding(otID, resourceID))
	}
	vbs.EXPECT().GetResourcesByIDs(gomock.Any(), gomock.Any()).Return(nil, forbidden("GetResourcesByIDs")).Times(2)
	vbs.EXPECT().GetResourceByID(gomock.Any(), gomock.Any()).Return(nil, forbidden("GetResourceByID")).Times(total)
	resolver.EXPECT().ResolveKNProxyBindings(gomock.Any(), "kn1", bindings).Return(readyProxy(), bindings, nil)
	var sizes []int
	vbs.EXPECT().GetResourcesByIDsAs(gomock.Any(), proxyAccount, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interfaces.AccountInfo, ids []string) ([]*interfaces.VegaResource, error) {
			sizes = append(sizes, len(ids))
			out := make([]*interfaces.VegaResource, 0, len(ids))
			for _, id := range ids {
				out = append(out, indexedResource(id))
			}
			return out, nil
		}).Times(2)

	if err := service.enrichObjectTypes(readerContext(), objectTypes); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{vegaResourceBatchSize, 3}) {
		t.Fatalf("proxy read sizes = %v", sizes)
	}
	for i, objectType := range objectTypes {
		assertEnrichedFrom(t, objectType, fmt.Sprintf("r%d", i))
	}
}

func TestRegisteredKNProxyResolverIsUsedWithoutOverride(t *testing.T) {
	resolver := bmock.NewMockKNProxyBindingResolver(gomock.NewController(t))
	RegisterKNProxyBindingResolver(resolver)
	t.Cleanup(func() { RegisterKNProxyBindingResolver(nil) })

	if got := (&objectTypeService{}).knProxyResolver(); got != resolver {
		t.Fatalf("resolver = %v, want the registered one", got)
	}
}

func TestInsertDatasetDataNeverIndexesTheMarker(t *testing.T) {
	ctrl := gomock.NewController(t)
	vbs := bmock.NewMockVegaBackendService(ctrl)
	service := &objectTypeService{appSetting: &common.AppSetting{}, vbs: vbs}
	objectType := boundObjectType("ot1", "r1", interfaces.MAIN_BRANCH)
	objectType.DataSourceMetadataUnavailable = true
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, document map[string]any) error {
			if _, ok := document["data_source_metadata_unavailable"]; ok {
				t.Fatalf("indexed document carries the marker: %v", document)
			}
			return nil
		})

	if err := service.InsertDatasetData(context.Background(), []*interfaces.ObjectType{objectType}); err != nil {
		t.Fatal(err)
	}
}
