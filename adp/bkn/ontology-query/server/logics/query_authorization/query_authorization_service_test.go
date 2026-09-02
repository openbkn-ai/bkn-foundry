// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

func TestAuthorizeMetricQueryUsesPublishedDependencies(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetMetricDefinition(gomock.Any(), "kn-a", "main", "metric-1").Return(
		&interfaces.MetricDefinition{
			ID: "metric-1", KnID: "kn-a", Branch: "main",
			ScopeType: interfaces.ScopeTypeObjectType, ScopeRef: "orders",
		}, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "metric", ID: "kn-a/metric-1"},
		{Type: "object_type", ID: "kn-a/orders"},
		{Type: "resource", ID: "orders-resource"},
	}).Return(nil)

	if err := service.AuthorizeMetricQuery(context.Background(), "kn-a", "main", "metric-1"); err != nil {
		t.Fatalf("AuthorizeMetricQuery() error = %v", err)
	}
}

func TestAuthorizeMetricDryRunDoesNotInventMetricResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
		{Type: "resource", ID: "orders-resource"},
	}).Return(nil)

	err := service.AuthorizeMetricDryRun(context.Background(), "kn-a", "main", &interfaces.MetricDefinition{
		KnID: "kn-a", ScopeType: interfaces.ScopeTypeObjectType, ScopeRef: "orders",
	})
	if err != nil {
		t.Fatalf("AuthorizeMetricDryRun() error = %v", err)
	}
}

func TestAuthorizeSubgraphBySourceUsesServerResolvedPaths(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "customer").Return(
		publishedObjectType("kn-a", "customer", "customer-resource"), true, nil)
	models.EXPECT().GetRelationTypePathsBaseOnSource(gomock.Any(), "kn-a", "main", gomock.Any()).Return(
		[]interfaces.RelationTypePath{{
			ObjectTypes: []interfaces.ObjectTypeWithKeyField{
				{OTID: "customer", DataSource: resourceInfo("customer-resource")},
				{OTID: "order", DataSource: resourceInfo("order-resource")},
			},
			TypeEdges: []interfaces.TypeEdge{{
				RelationTypeId: "places",
				RelationType:   interfaces.RelationType{RTID: "places", Type: interfaces.RELATION_TYPE_DIRECT},
			}},
		}}, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "order").Return(
		publishedObjectType("kn-a", "order", "order-resource"), true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "places").Return(
		interfaces.RelationType{RTID: "places", Type: interfaces.RELATION_TYPE_DIRECT}, true, nil)
	requested := []interfaces.PermissionResource{
		{Type: "object_type", ID: "kn-a/customer"},
		{Type: "resource", ID: "customer-resource"},
		{Type: "object_type", ID: "kn-a/customer"},
		{Type: "resource", ID: "customer-resource"},
		{Type: "object_type", ID: "kn-a/order"},
		{Type: "resource", ID: "order-resource"},
		{Type: "relation_type", ID: "kn-a/places"},
	}
	permissions.EXPECT().FilterQueryData(gomock.Any(), requested).Return(requested, nil)

	query := &interfaces.SubGraphQueryBaseOnSource{
		KNID: "kn-a", Branch: "main", SourceObjecTypeId: "customer", Direction: "forward", PathLength: 1,
	}
	err := service.AuthorizeSubgraphBySource(context.Background(), query)
	if err != nil {
		t.Fatalf("AuthorizeSubgraphBySource() error = %v", err)
	}
	if len(query.AuthorizedTypePaths) != 1 {
		t.Fatalf("AuthorizedTypePaths = %d, want 1", len(query.AuthorizedTypePaths))
	}
}

func TestAuthorizeSubgraphBySourceFiltersDeniedCandidatePath(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "customer").Return(
		publishedObjectType("kn-a", "customer", "customer-resource"), true, nil)
	allowedPath := publishedPath("customer", "customer-resource", "order", "order-resource", "places")
	deniedPath := publishedPath("customer", "customer-resource", "secret", "secret-resource", "owns-secret")
	models.EXPECT().GetRelationTypePathsBaseOnSource(gomock.Any(), "kn-a", "main", gomock.Any()).Return(
		[]interfaces.RelationTypePath{allowedPath, deniedPath}, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "order").Return(
		publishedObjectType("kn-a", "order", "order-resource"), true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "secret").Return(
		publishedObjectType("kn-a", "secret", "secret-resource"), true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "places").Return(
		interfaces.RelationType{RTID: "places", Type: interfaces.RELATION_TYPE_DIRECT}, true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "owns-secret").Return(
		interfaces.RelationType{RTID: "owns-secret", Type: interfaces.RELATION_TYPE_DIRECT}, true, nil)
	permissions.EXPECT().FilterQueryData(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, requested []interfaces.PermissionResource) ([]interfaces.PermissionResource, error) {
			allowed := make([]interfaces.PermissionResource, 0, len(requested))
			for _, resource := range requested {
				if resource.ID != "kn-a/secret" && resource.ID != "secret-resource" && resource.ID != "kn-a/owns-secret" {
					allowed = append(allowed, resource)
				}
			}
			return allowed, nil
		})

	query := &interfaces.SubGraphQueryBaseOnSource{
		KNID: "kn-a", Branch: "main", SourceObjecTypeId: "customer", Direction: "forward", PathLength: 1,
	}
	if err := service.AuthorizeSubgraphBySource(context.Background(), query); err != nil {
		t.Fatalf("AuthorizeSubgraphBySource() error = %v", err)
	}
	if len(query.AuthorizedTypePaths) != 1 || query.AuthorizedTypePaths[0].TypeEdges[0].RelationTypeId != "places" {
		t.Fatalf("AuthorizedTypePaths = %#v", query.AuthorizedTypePaths)
	}
}

func publishedObjectType(knID, objectTypeID, resourceID string) interfaces.ObjectType {
	return interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:       objectTypeID,
			DataSource: resourceInfo(resourceID),
		},
		KNID: knID, Branch: interfaces.MAIN_BRANCH,
	}
}

func resourceInfo(resourceID string) *interfaces.ResourceInfo {
	return &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: resourceID}
}

func publishedPath(sourceID, sourceResourceID, targetID, targetResourceID,
	relationTypeID string) interfaces.RelationTypePath {
	return interfaces.RelationTypePath{
		ObjectTypes: []interfaces.ObjectTypeWithKeyField{
			{OTID: sourceID, DataSource: resourceInfo(sourceResourceID)},
			{OTID: targetID, DataSource: resourceInfo(targetResourceID)},
		},
		TypeEdges: []interfaces.TypeEdge{{
			RelationTypeId: relationTypeID,
			RelationType:   interfaces.RelationType{RTID: relationTypeID, Type: interfaces.RELATION_TYPE_DIRECT},
		}},
	}
}

func TestAuthorizeObjectTypeRejectsUnpublishedBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := &queryAuthorizationService{
		models:      omock.NewMockOntologyManagerAccess(ctrl),
		permissions: omock.NewMockPermissionService(ctrl),
	}
	err := service.AuthorizeObjectTypeQuery(context.Background(), "kn-a", "draft", "orders")
	assertQueryAuthHTTPStatus(t, err, http.StatusBadRequest)
}

func TestAuthorizeObjectTypeFailsClosedOnModelLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	service := &queryAuthorizationService{
		models:      models,
		permissions: omock.NewMockPermissionService(ctrl),
	}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		interfaces.ObjectType{}, false, errors.New("timeout"))
	err := service.AuthorizeObjectTypeQuery(context.Background(), "kn-a", "main", "orders")
	assertQueryAuthHTTPStatus(t, err, http.StatusServiceUnavailable)
}

func TestAuthorizeObjectTypeScopesSameChildIDByKnowledgeNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	for _, knID := range []string{"kn-a", "kn-b"} {
		models.EXPECT().GetObjectType(gomock.Any(), knID, "main", "orders").Return(
			publishedObjectType(knID, "orders", knID+"-orders-resource"), true, nil)
		permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
			{Type: "object_type", ID: knID + "/orders"},
			{Type: "resource", ID: knID + "-orders-resource"},
		}).Return(nil)
		if err := service.AuthorizeObjectTypeQuery(context.Background(), knID, "main", "orders"); err != nil {
			t.Fatalf("AuthorizeObjectTypeQuery(%s) error = %v", knID, err)
		}
	}
}

func assertQueryAuthHTTPStatus(t *testing.T, err error, expected int) {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if httpErr.HTTPCode != expected {
		t.Fatalf("HTTP status = %d, want %d", httpErr.HTTPCode, expected)
	}
}
