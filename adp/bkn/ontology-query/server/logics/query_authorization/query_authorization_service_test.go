// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

func TestAuthorizeObjectMetricQueryDoesNotConsultLegacyMetricCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{permissions: permissions}

	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "metric", ID: "kn-a/metric-v1"},
	}).Return(nil)

	if err := service.AuthorizeObjectMetricQuery(context.Background(), "kn-a", "main", "metric-v1"); err != nil {
		t.Fatalf("AuthorizeObjectMetricQuery() error = %v", err)
	}
}

func TestAuthorizeObjectTypeSchemaRequiresViewDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	permissions.EXPECT().RequirePermissions(gomock.Any(), []interfaces.PermissionRequirement{{
		ResourceType: interfaces.PermissionResourceTypeObjectType,
		ResourceID:   "kn-a/orders",
		Operation:    interfaces.PermissionOperationViewDetail,
	}}).Return(nil)

	if err := service.AuthorizeObjectTypeSchema(context.Background(), "kn-a", "main", "orders"); err != nil {
		t.Fatalf("AuthorizeObjectTypeSchema() error = %v", err)
	}
}

func TestAuthorizeObjectMetricTrialUsesKnowledgeNetworkAndObjectResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
	}).Return(nil)

	err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
		OwnerObjectTypeID: "orders",
	}, nil)
	if err != nil {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v", err)
	}
}

func TestAuthorizeObjectMetricTrialRequiresEveryReferencedResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "order-customer").Return(
		interfaces.RelationType{
			RTID: "order-customer", Type: interfaces.RELATION_TYPE_DIRECT,
			SourceObjectTypeID: "orders", TargetObjectTypeID: "customers",
		}, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "customers").Return(
		publishedObjectType("kn-a", "customers", "customers-resource"), true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "order-region").Return(
		interfaces.RelationType{
			RTID: "order-region", Type: interfaces.RELATION_TYPE_DIRECT,
			SourceObjectTypeID: "orders", TargetObjectTypeID: "regions",
		}, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "regions").Return(
		publishedObjectType("kn-a", "regions", "regions-resource"), true, nil)

	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
		{Type: "relation_type", ID: "kn-a/order-customer"},
		{Type: "object_type", ID: "kn-a/customers"},
		{Type: "metric", ID: "kn-a/base-metric"},
		{Type: "metric", ID: "kn-a/dependency-metric"},
		{Type: "relation_type", ID: "kn-a/order-region"},
		{Type: "object_type", ID: "kn-a/regions"},
	}).Return(nil)

	err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
		OwnerObjectTypeID: "orders",
		Specification: interfaces.ObjectMetricSpecificationV1{
			BaseMetric: &interfaces.ObjectMetricRefV1{MetricID: "base-metric", Version: 2},
			BusinessQualifiers: &interfaces.ObjectMetricBusinessQualifiersV1{
				RelationPath: &interfaces.ObjectMetricRelationPathV1{Steps: []interfaces.ObjectMetricRelationStepV1{{
					RelationTypeID: "order-customer", Direction: interfaces.DIRECTION_FORWARD,
					TargetObjectTypeID: "customers",
				}}},
			},
			Dependencies: []interfaces.ObjectMetricDependencyV1{{
				MetricRef: interfaces.ObjectMetricRefV1{MetricID: "dependency-metric", Version: 3},
				ObjectAlignment: interfaces.ObjectMetricObjectAlignmentV1{
					RelationPath: []interfaces.ObjectMetricRelationStepV1{{
						RelationTypeID: "order-region", Direction: interfaces.DIRECTION_FORWARD,
						TargetObjectTypeID: "regions",
					}},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v", err)
	}
}

func TestAuthorizeObjectMetricTrialRequiresFullPropertyAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	properties := omock.NewMockPropertyAccessService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions, properties: properties}

	orders := publishedObjectType("kn-a", "orders", "orders-resource")
	orders.DataProperties = []cond.DataProperty{{Name: "amount"}, {Name: "status"}}
	customers := publishedObjectType("kn-a", "customers", "customers-resource")
	customers.DataProperties = []cond.DataProperty{{Name: "region"}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(orders, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "customers").Return(customers, true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
		{Type: "object_type", ID: "kn-a/customers"},
	}).Return(nil)
	properties.EXPECT().ResolvePropertyLevels(gomock.Any(), []interfaces.PropertyLevelsRequestItem{
		{ObjectTypeRef: "kn-a/customers", Properties: []string{"region"}},
		{ObjectTypeRef: "kn-a/orders", Properties: []string{"amount", "status"}},
	}).Return([]interfaces.PropertyLevelsDecisionEntry{
		{ObjectTypeRef: "kn-a/customers", Properties: []interfaces.PropertyAccessDecision{{Name: "region", Level: interfaces.PropertyAccessFull}}},
		{ObjectTypeRef: "kn-a/orders", Properties: []interfaces.PropertyAccessDecision{
			{Name: "amount", Level: interfaces.PropertyAccessFull},
			{Name: "status", Level: interfaces.PropertyAccessFull},
		}},
	}, nil)

	err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
		OwnerObjectTypeID: "orders",
		Specification: interfaces.ObjectMetricSpecificationV1{
			Measure: &interfaces.ObjectMetricMeasureV1{Expression: &interfaces.ObjectMetricExpressionV1{
				PropertyRef: &interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "orders", PropertyName: "amount"},
			}},
			Alignment: &interfaces.ObjectMetricAlignmentV1{
				ResultOwnerObjectTypeID: "customers",
			},
		},
	}, &interfaces.ObjectMetricQueryRequestV1{
		Filter: &interfaces.ObjectMetricConditionV1{Left: &interfaces.ObjectMetricOperandV1{
			PropertyRef: &interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "orders", PropertyName: "status"},
		}},
		GroupBy: []interfaces.ObjectMetricPropertyRefV1{{ObjectTypeID: "customers", PropertyName: "region"}},
	})
	if err != nil {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v", err)
	}
}

func TestAuthorizeObjectMetricTrialRejectsNonFullPropertyAccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	properties := omock.NewMockPropertyAccessService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions, properties: properties}

	orders := publishedObjectType("kn-a", "orders", "orders-resource")
	orders.DataProperties = []cond.DataProperty{{Name: "amount"}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(orders, true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
	}).Return(nil)
	properties.EXPECT().ResolvePropertyLevels(gomock.Any(), []interfaces.PropertyLevelsRequestItem{
		{ObjectTypeRef: "kn-a/orders", Properties: []string{"amount"}},
	}).Return([]interfaces.PropertyLevelsDecisionEntry{{
		ObjectTypeRef: "kn-a/orders",
		Properties:    []interfaces.PropertyAccessDecision{{Name: "amount", Level: interfaces.PropertyAccessSchema}},
	}}, nil)

	err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
		OwnerObjectTypeID: "orders",
		Specification: interfaces.ObjectMetricSpecificationV1{Measure: &interfaces.ObjectMetricMeasureV1{
			Expression: &interfaces.ObjectMetricExpressionV1{PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
				ObjectTypeID: "orders", PropertyName: "amount",
			}},
		}},
	}, nil)
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v, want 403", err)
	}
}

func TestAuthorizeObjectMetricTrialRejectsNonFullQueryFilterProperty(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	properties := omock.NewMockPropertyAccessService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions, properties: properties}

	orders := publishedObjectType("kn-a", "orders", "orders-resource")
	orders.DataProperties = []cond.DataProperty{{Name: "amount"}, {Name: "status"}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(orders, true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
	}).Return(nil)
	properties.EXPECT().ResolvePropertyLevels(gomock.Any(), []interfaces.PropertyLevelsRequestItem{
		{ObjectTypeRef: "kn-a/orders", Properties: []string{"amount", "status"}},
	}).Return([]interfaces.PropertyLevelsDecisionEntry{{
		ObjectTypeRef: "kn-a/orders",
		Properties: []interfaces.PropertyAccessDecision{
			{Name: "amount", Level: interfaces.PropertyAccessFull},
			{Name: "status", Level: interfaces.PropertyAccessSchema},
		},
	}}, nil)

	err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
		OwnerObjectTypeID: "orders",
		Specification: interfaces.ObjectMetricSpecificationV1{Measure: &interfaces.ObjectMetricMeasureV1{
			Expression: &interfaces.ObjectMetricExpressionV1{PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
				ObjectTypeID: "orders", PropertyName: "amount",
			}},
		}},
	}, &interfaces.ObjectMetricQueryRequestV1{Filter: &interfaces.ObjectMetricConditionV1{
		Left: &interfaces.ObjectMetricOperandV1{PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
			ObjectTypeID: "orders", PropertyName: "status",
		}},
	}})
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v, want 403", err)
	}
}

func TestAuthorizeObjectMetricTrialRejectsNonFullParentQueryProperties(t *testing.T) {
	for _, test := range []struct {
		name        string
		parentQuery interfaces.ObjectMetricQueryRequestV1
	}{
		{
			name: "parent filter",
			parentQuery: interfaces.ObjectMetricQueryRequestV1{Filter: &interfaces.ObjectMetricConditionV1{
				Left: &interfaces.ObjectMetricOperandV1{PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
					ObjectTypeID: "orders", PropertyName: "status",
				}},
			}},
		},
		{
			name: "parent analysis dimension",
			parentQuery: interfaces.ObjectMetricQueryRequestV1{GroupBy: []interfaces.ObjectMetricPropertyRefV1{{
				ObjectTypeID: "orders", PropertyName: "status",
			}}},
		},
		{
			name: "ancestor filter",
			parentQuery: interfaces.ObjectMetricQueryRequestV1{BucketSelection: &interfaces.ObjectMetricBucketSelectionV1{
				ParentQuery: interfaces.ObjectMetricQueryRequestV1{Filter: &interfaces.ObjectMetricConditionV1{
					Left: &interfaces.ObjectMetricOperandV1{PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
						ObjectTypeID: "orders", PropertyName: "status",
					}},
				}},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			permissions := omock.NewMockPermissionService(ctrl)
			properties := omock.NewMockPropertyAccessService(ctrl)
			service := &queryAuthorizationService{models: models, permissions: permissions, properties: properties}

			orders := publishedObjectType("kn-a", "orders", "orders-resource")
			orders.DataProperties = []cond.DataProperty{{Name: "amount"}, {Name: "status"}}
			models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(orders, true, nil)
			permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
				{Type: "knowledge_network", ID: "kn-a"},
				{Type: "object_type", ID: "kn-a/orders"},
			}).Return(nil)
			properties.EXPECT().ResolvePropertyLevels(gomock.Any(), []interfaces.PropertyLevelsRequestItem{
				{ObjectTypeRef: "kn-a/orders", Properties: []string{"amount", "status"}},
			}).Return([]interfaces.PropertyLevelsDecisionEntry{{
				ObjectTypeRef: "kn-a/orders",
				Properties: []interfaces.PropertyAccessDecision{
					{Name: "amount", Level: interfaces.PropertyAccessFull},
					{Name: "status", Level: interfaces.PropertyAccessSchema},
				},
			}}, nil)

			definition := &interfaces.ObjectMetricDefinitionV1{
				OwnerObjectTypeID: "orders",
				Specification: interfaces.ObjectMetricSpecificationV1{Measure: &interfaces.ObjectMetricMeasureV1{
					Expression: &interfaces.ObjectMetricExpressionV1{PropertyRef: &interfaces.ObjectMetricPropertyRefV1{
						ObjectTypeID: "orders", PropertyName: "amount",
					}},
				}},
			}
			query := &interfaces.ObjectMetricQueryRequestV1{BucketSelection: &interfaces.ObjectMetricBucketSelectionV1{
				ParentQuery: test.parentQuery,
				BucketKey:   "server-issued-key",
			}}
			err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", definition, query)
			var httpErr *rest.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
				t.Fatalf("parent query must require full property access: %v", err)
			}
		})
	}
}

func TestObjectMetricPropertyRequirementsRejectsExcessiveBucketSelectionDepth(t *testing.T) {
	query := interfaces.ObjectMetricQueryRequestV1{}
	for range 9 {
		query = interfaces.ObjectMetricQueryRequestV1{BucketSelection: &interfaces.ObjectMetricBucketSelectionV1{ParentQuery: query}}
	}
	_, err := objectMetricPropertyRequirements(&interfaces.ObjectMetricDefinitionV1{}, &query)
	if err == nil {
		t.Fatal("excessive parent query depth must be rejected before authorization")
	}
}

func TestAuthorizeObjectMetricTrialRejectsRelationPathOutsidePublishedModel(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	service := &queryAuthorizationService{models: models}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "order-customer").Return(
		interfaces.RelationType{
			RTID: "order-customer", Type: interfaces.RELATION_TYPE_DIRECT,
			SourceObjectTypeID: "orders", TargetObjectTypeID: "customers",
		}, true, nil)

	err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
		OwnerObjectTypeID: "orders",
		Specification: interfaces.ObjectMetricSpecificationV1{
			BusinessQualifiers: &interfaces.ObjectMetricBusinessQualifiersV1{
				RelationPath: &interfaces.ObjectMetricRelationPathV1{Steps: []interfaces.ObjectMetricRelationStepV1{{
					RelationTypeID: "order-customer", Direction: interfaces.DIRECTION_FORWARD,
					TargetObjectTypeID: "restricted-object-type",
				}}},
			},
		},
	}, nil)
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v, want 400", err)
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
		{Type: "object_type", ID: "kn-a/customer"},
		{Type: "object_type", ID: "kn-a/order"},
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
				if resource.ID != "kn-a/secret" && resource.ID != "kn-a/owns-secret" {
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

// A path may walk a relation against its definition. Authorization used to demand
// that every edge run from the relation's source to its target, so with query
// authorization on, every backward hop was refused as outside the published model.
func TestAuthorizeSubgraphByTypePathAcceptsBackwardEdge(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "user").Return(
		publishedObjectType("kn-a", "user", "user-resource"), true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "order").Return(
		publishedObjectType("kn-a", "order", "order-resource"), true, nil)
	models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "rel_order_user").Return(
		orderUserRelation(), true, nil)
	permissions.EXPECT().FilterQueryData(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, requested []interfaces.PermissionResource) ([]interfaces.PermissionResource, error) {
			return requested, nil
		})

	query := userOrderPathQuery(interfaces.TypeEdge{
		RelationTypeId: "rel_order_user", SourceObjectTypeId: "user", TargetObjectTypeId: "order",
	})
	if err := service.AuthorizeSubgraphByTypePath(context.Background(), query); err != nil {
		t.Fatalf("AuthorizeSubgraphByTypePath() error = %v", err)
	}
	if len(query.Paths.TypePaths) != 1 {
		t.Fatalf("retained paths = %d, want 1", len(query.Paths.TypePaths))
	}
}

func TestAuthorizeSubgraphByTypePathRejectsEdgeThatCannotWalkRelation(t *testing.T) {
	tests := []struct {
		name     string
		relation interfaces.RelationType
		edge     interfaces.TypeEdge
	}{
		{
			name: "endpoints are not the relation's ends",
			relation: interfaces.RelationType{RTID: "rel_address_user", Type: interfaces.RELATION_TYPE_DIRECT,
				SourceObjectTypeID: "user_address", TargetObjectTypeID: "user"},
			edge: interfaces.TypeEdge{RelationTypeId: "rel_address_user", SourceObjectTypeId: "user", TargetObjectTypeId: "order"},
		},
		{
			name:     "declared direction contradicts the endpoints",
			relation: orderUserRelation(),
			edge: interfaces.TypeEdge{RelationTypeId: "rel_order_user", SourceObjectTypeId: "user", TargetObjectTypeId: "order",
				Direction: interfaces.DIRECTION_FORWARD},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			permissions := omock.NewMockPermissionService(ctrl)
			service := &queryAuthorizationService{models: models, permissions: permissions}

			models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", gomock.Any()).DoAndReturn(
				func(_ context.Context, knID, _ string, objectTypeID string) (interfaces.ObjectType, bool, error) {
					return publishedObjectType(knID, objectTypeID, objectTypeID+"-resource"), true, nil
				}).Times(2)
			models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", tt.relation.RTID).Return(tt.relation, true, nil)

			err := service.AuthorizeSubgraphByTypePath(context.Background(), userOrderPathQuery(tt.edge))
			var httpErr *rest.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
				t.Fatalf("AuthorizeSubgraphByTypePath() error = %v, want 400", err)
			}
		})
	}
}

func orderUserRelation() interfaces.RelationType {
	return interfaces.RelationType{RTID: "rel_order_user", Type: interfaces.RELATION_TYPE_DIRECT,
		SourceObjectTypeID: "order", TargetObjectTypeID: "user"}
}

func userOrderPathQuery(edge interfaces.TypeEdge) *interfaces.SubGraphQueryBaseOnTypePath {
	return &interfaces.SubGraphQueryBaseOnTypePath{
		KNID: "kn-a", Branch: "main",
		Paths: interfaces.QueryRelationTypePaths{TypePaths: []interfaces.QueryRelationTypePath{{
			ObjectTypes: []interfaces.ObjectTypeWithKeyField{{OTID: "user"}, {OTID: "order"}},
			Edges:       []interfaces.TypeEdge{edge},
		}}},
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

func TestObjectTypeResourcesSupportsLegacyResourceBindingWithoutType(t *testing.T) {
	objectType := publishedObjectType("kn-a", "orders", "orders-resource")
	objectType.DataSource.Type = ""

	resources, err := objectTypeResources(context.Background(), "kn-a", objectType)
	if err != nil {
		t.Fatalf("objectTypeResources() error = %v", err)
	}
	want := interfaces.KNChildPermissionResource(interfaces.PermissionResourceTypeObjectType, "kn-a", "orders")
	if len(resources) != 1 || resources[0] != want {
		t.Fatalf("objectTypeResources() = %#v, want %#v", resources, want)
	}
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

func TestAuthorizeObjectTypePreservesModelPermissionDenial(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	service := &queryAuthorizationService{
		models:      models,
		permissions: omock.NewMockPermissionService(ctrl),
	}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		interfaces.ObjectType{}, false,
		rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))

	err := service.AuthorizeObjectTypeQuery(context.Background(), "kn-a", "main", "orders")
	assertQueryAuthHTTPStatus(t, err, http.StatusForbidden)
}

func TestAuthorizeActionTypePreservesWrappedModelPermissionDenial(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	service := &queryAuthorizationService{models: models, permissions: omock.NewMockPermissionService(ctrl)}

	models.EXPECT().GetActionType(gomock.Any(), "kn-a", "main", "action-1").Return(
		interfaces.ActionType{}, nil, false,
		fmt.Errorf("get action type failed: %w",
			rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)))

	err := service.AuthorizeActionTypeQuery(context.Background(), "kn-a", "main", "action-1")
	assertQueryAuthHTTPStatus(t, err, http.StatusForbidden)
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
		}).Return(nil)
		if err := service.AuthorizeObjectTypeQuery(context.Background(), knID, "main", "orders"); err != nil {
			t.Fatalf("AuthorizeObjectTypeQuery(%s) error = %v", knID, err)
		}
	}
}

func TestAuthorizeObjectTypeQueryDelegatesResourceCatalogFallbackToVega(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "object_type", ID: "kn-a/orders"},
	}).Return(nil)

	if err := service.AuthorizeObjectTypeQuery(context.Background(), "kn-a", "main", "orders"); err != nil {
		t.Fatalf("AuthorizeObjectTypeQuery() error = %v", err)
	}
}

func TestAuthorizeObjectTypeQueryFailsClosedWhenPublishedResourceIsMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	service := &queryAuthorizationService{
		models:      models,
		permissions: omock.NewMockPermissionService(ctrl),
	}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "orders"},
			KNID:                   "kn-a",
			Branch:                 "main",
		}, true, nil)

	err := service.AuthorizeObjectTypeQuery(context.Background(), "kn-a", "main", "orders")
	assertQueryAuthHTTPStatus(t, err, http.StatusServiceUnavailable)
}

func TestAuthorizeSubgraphByObjectsFailsClosedWithoutModelAccess(t *testing.T) {
	var service *queryAuthorizationService
	err := service.AuthorizeSubgraphByObjects(context.Background(), &interfaces.SubGraphQueryBaseOnObjects{
		KNID: "kn-a", Branch: "main",
	})
	assertQueryAuthHTTPStatus(t, err, http.StatusServiceUnavailable)
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
