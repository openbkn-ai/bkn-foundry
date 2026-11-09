// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
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

func TestAuthorizeObjectMetricExecutionRejectsMissingObjectQueryData(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "metric", ID: "kn-a/sales"},
		{Type: "object_type", ID: "kn-a/orders"},
	}).Return(httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden))

	err := service.AuthorizeObjectMetricExecution(context.Background(), "kn-a", "main", objectMetricExecutionContext(
		"sales", "orders", nil), nil)
	assertQueryAuthHTTPStatus(t, err, http.StatusForbidden)
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || strings.Contains(fmt.Sprint(httpErr.ErrorDetails), "orders") {
		t.Fatalf("permission error leaked protected resource details: %v", err)
	}
}

func TestAuthorizeObjectMetricExecutionRequiresFullPropertyAccess(t *testing.T) {
	for _, test := range []struct {
		name      string
		level     interfaces.PropertyAccessLevel
		wantError bool
	}{
		{name: "none is forbidden", level: interfaces.PropertyAccessNone, wantError: true},
		{name: "full is allowed", level: interfaces.PropertyAccessFull},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			permissions := omock.NewMockPermissionService(ctrl)
			properties := omock.NewMockPropertyAccessService(ctrl)
			service := &queryAuthorizationService{models: models, permissions: permissions, properties: properties}

			orders := publishedObjectType("kn-a", "orders", "orders-resource")
			orders.DataProperties = []cond.DataProperty{{Name: "amount"}}
			models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(orders, true, nil)
			permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
				{Type: "metric", ID: "kn-a/sales"},
				{Type: "object_type", ID: "kn-a/orders"},
			}).Return(nil)
			properties.EXPECT().ResolvePropertyLevels(gomock.Any(), []interfaces.PropertyLevelsRequestItem{{
				ObjectTypeRef: "kn-a/orders", Properties: []string{"amount"},
			}}).Return([]interfaces.PropertyLevelsDecisionEntry{{
				ObjectTypeRef: "kn-a/orders",
				Properties:    []interfaces.PropertyAccessDecision{{Name: "amount", Level: test.level}},
			}}, nil)

			err := service.AuthorizeObjectMetricExecution(context.Background(), "kn-a", "main", objectMetricExecutionContext(
				"sales", "orders", &interfaces.ObjectMetricPropertyRefV1{
					ObjectTypeID: "orders", PropertyName: "amount",
				}), nil)
			if !test.wantError {
				if err != nil {
					t.Fatalf("AuthorizeObjectMetricExecution() error = %v", err)
				}
				return
			}
			assertQueryAuthHTTPStatus(t, err, http.StatusForbidden)
		})
	}
}

func TestAuthorizeObjectMetricExecutionRequiresDependencyClosure(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	service := &queryAuthorizationService{models: models, permissions: permissions}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "customers").Return(
		publishedObjectType("kn-a", "customers", "customers-resource"), true, nil)
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "metric", ID: "kn-a/composite-sales"},
		{Type: "metric", ID: "kn-a/customer-sales"},
		{Type: "object_type", ID: "kn-a/orders"},
		{Type: "object_type", ID: "kn-a/customers"},
	}).Return(nil)

	executionContext := objectMetricExecutionContext("composite-sales", "orders", nil)
	executionContext.Root.Definition.Specification.Dependencies = []interfaces.ObjectMetricDependencyV1{{
		MetricRef: interfaces.ObjectMetricRefV1{MetricID: "customer-sales", Version: 1},
	}}
	executionContext.Dependencies = []*interfaces.ObjectMetricRecordV1{{
		KNID: "kn-a", Branch: "main",
		Definition: interfaces.ObjectMetricDefinitionV1{
			ID: "customer-sales", Version: 1, OwnerObjectTypeID: "customers",
		},
	}}
	if err := service.AuthorizeObjectMetricExecution(context.Background(), "kn-a", "main", executionContext, nil); err != nil {
		t.Fatalf("AuthorizeObjectMetricExecution() error = %v", err)
	}
}

func objectMetricExecutionContext(metricID, ownerObjectTypeID string,
	measure *interfaces.ObjectMetricPropertyRefV1) *interfaces.ObjectMetricExecutionContextV1 {
	definition := interfaces.ObjectMetricDefinitionV1{
		ID: metricID, Version: 1, OwnerObjectTypeID: ownerObjectTypeID,
	}
	if measure != nil {
		definition.Specification.Measure = &interfaces.ObjectMetricMeasureV1{
			Expression: &interfaces.ObjectMetricExpressionV1{PropertyRef: measure},
		}
	}
	return &interfaces.ObjectMetricExecutionContextV1{Root: &interfaces.ObjectMetricRecordV1{
		Definition: definition, KNID: "kn-a", Branch: "main",
	}}
}

type fixedMetricRowFilters struct {
	digest string
}

type fixedMetricProxy struct {
	version int64
}

func (proxy fixedMetricProxy) Resolve(_ context.Context, binding interfaces.TrustedProxyBinding) (*interfaces.TrustedProxyContext, error) {
	return &interfaces.TrustedProxyContext{Proxy: interfaces.AccountInfo{ID: "proxy", Type: "app"},
		ProxyVersion: proxy.version, PublishedModelVersion: "model-v1", Binding: binding}, nil
}

func (filters fixedMetricRowFilters) ResolveRowFilters(_ context.Context,
	refs []string) ([]interfaces.RowFilterDecisionEntry, error) {
	entries := make([]interfaces.RowFilterDecisionEntry, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, interfaces.RowFilterDecisionEntry{
			ObjectTypeRef:            ref,
			Predicate:                interfaces.RowFilterPredicate{Kind: "true"},
			EffectiveRowFilterDigest: filters.digest,
		})
	}
	return entries, nil
}

func TestObjectMetricAuthorizationFingerprintChangesWithRowFilter(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").AnyTimes().Return(
		publishedObjectType("kn-a", "orders", "orders-resource"), true, nil)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	definition := &interfaces.ObjectMetricDefinitionV1{OwnerObjectTypeID: "orders"}
	definition.Specification.Measure = &interfaces.ObjectMetricMeasureV1{Expression: &interfaces.ObjectMetricExpressionV1{
		PropertyRef: &interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "orders", PropertyName: "amount"},
	}}
	input := []objectMetricAuthorizationInput{{definition: definition}}
	first, err := (&queryAuthorizationService{models: models, snapshotProxies: allowMetricSnapshotProxies{}, proxy: fixedMetricProxy{version: 1}, rowFilters: fixedMetricRowFilters{digest: "sha256:a"}}).
		objectMetricAuthorizationFingerprint(ctx, "kn-a", input)
	if err != nil || first.Digest == "" {
		t.Fatalf("first fingerprint = %#v, %v", first, err)
	}
	second, err := (&queryAuthorizationService{models: models, snapshotProxies: allowMetricSnapshotProxies{}, proxy: fixedMetricProxy{version: 1}, rowFilters: fixedMetricRowFilters{digest: "sha256:b"}}).
		objectMetricAuthorizationFingerprint(ctx, "kn-a", input)
	if err != nil || first.Digest == second.Digest {
		t.Fatalf("row-filter change did not change fingerprint: first=%q second=%q error=%v",
			first.Digest, second.Digest, err)
	}
	third, err := (&queryAuthorizationService{models: models, snapshotProxies: allowMetricSnapshotProxies{}, proxy: fixedMetricProxy{version: 2}, rowFilters: fixedMetricRowFilters{digest: "sha256:a"}}).
		objectMetricAuthorizationFingerprint(ctx, "kn-a", input)
	if err != nil || third.Digest == first.Digest {
		t.Fatalf("proxy version was not bound: %#v %v", third, err)
	}
}

type fixedMetricProperties struct {
	level interfaces.PropertyAccessLevel
}

func (properties fixedMetricProperties) ResolvePropertyLevels(_ context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	entries := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	for _, item := range items {
		entry := interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, interfaces.PropertyAccessDecision{Name: name, Level: properties.level})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func TestMetricFingerprintBindsImplicitPropertiesModelAndCaller(t *testing.T) {
	models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
	model := publishedObjectType("kn-a", "orders", "resource")
	model.DataProperties = []cond.DataProperty{{Name: "join_key", Type: "string", MappedField: cond.Field{Name: "join_key"}}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").AnyTimes().DoAndReturn(
		func(context.Context, string, string, string) (interfaces.ObjectType, bool, error) {
			return model, true, nil
		})
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user-1", Type: "user"})
	service := &queryAuthorizationService{models: models, snapshotProxies: allowMetricSnapshotProxies{}, proxy: fixedMetricProxy{version: 1},
		properties: fixedMetricProperties{level: interfaces.PropertyAccessFull}, rowFilters: fixedMetricRowFilters{digest: "rows"}}
	inputs := []objectMetricAuthorizationInput{{definition: &interfaces.ObjectMetricDefinitionV1{OwnerObjectTypeID: "orders"}}}
	first, err := service.objectMetricAuthorizationFingerprint(ctx, "kn-a", inputs)
	if err != nil {
		t.Fatal(err)
	}
	model.Status = &interfaces.ObjectTypeStatus{DocCount: 1000, IndexAvailable: true}
	physicalChange, err := service.objectMetricAuthorizationFingerprint(ctx, "kn-a", inputs)
	if err != nil || physicalChange.Digest != first.Digest {
		t.Fatalf("physical index state changed authorization binding: %v", err)
	}
	service.properties = fixedMetricProperties{level: interfaces.PropertyAccessSchema}
	changedProperty, err := service.objectMetricAuthorizationFingerprint(ctx, "kn-a", inputs)
	if err != nil || changedProperty.Digest == first.Digest {
		t.Fatalf("implicit property was not bound: %v", err)
	}
	service.properties = fixedMetricProperties{level: interfaces.PropertyAccessFull}
	model.DataProperties[0].MappedField.Name = "other_column"
	changedModel, err := service.objectMetricAuthorizationFingerprint(ctx, "kn-a", inputs)
	if err != nil || changedModel.Digest == first.Digest {
		t.Fatalf("model mapping was not bound: %v", err)
	}
	model.DataProperties[0].MappedField.Name = "join_key"
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user-2", Type: "user"})
	changedCaller, err := service.objectMetricAuthorizationFingerprint(ctx, "kn-a", inputs)
	if err != nil || changedCaller.Digest == first.Digest {
		t.Fatalf("caller was not bound: %v", err)
	}
}

func TestMetricFingerprintRequiresAuthorizationBeforeReturningDigest(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	permissions := omock.NewMockPermissionService(ctrl)
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").Return(publishedObjectType("kn-a", "orders", "resource"), true, nil)
	denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
	permissions.EXPECT().RequireQueryData(gomock.Any(), gomock.Any()).Return(denied)
	service := &queryAuthorizationService{models: models, permissions: permissions}
	fingerprint, err := service.AuthorizeObjectMetricExecutionWithFingerprint(context.Background(), "kn-a", "main",
		objectMetricExecutionContext("metric", "orders", nil), nil)
	assertQueryAuthHTTPStatus(t, err, http.StatusForbidden)
	if fingerprint.Digest != "" {
		t.Fatal("denied execution received a fingerprint")
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
	permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
		{Type: "knowledge_network", ID: "kn-a"},
		{Type: "object_type", ID: "kn-a/orders"},
		{Type: "relation_type", ID: "kn-a/order-customer"},
		{Type: "object_type", ID: "kn-a/customers"},
		{Type: "metric", ID: "kn-a/base-metric"},
		{Type: "metric", ID: "kn-a/dependency-metric"},
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
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v", err)
	}
}

func TestAuthorizeObjectMetricTrialAcceptsLegacyRelationDirections(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		owner     string
		target    string
	}{
		{name: "outgoing", direction: "outgoing", owner: "orders", target: "customers"},
		{name: "incoming", direction: "incoming", owner: "customers", target: "orders"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			permissions := omock.NewMockPermissionService(ctrl)
			service := &queryAuthorizationService{models: models, permissions: permissions}

			models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", test.owner).Return(
				publishedObjectType("kn-a", test.owner, test.owner+"-resource"), true, nil)
			models.EXPECT().GetRelationType(gomock.Any(), "kn-a", "main", "order-customer").Return(
				interfaces.RelationType{
					RTID: "order-customer", Type: interfaces.RELATION_TYPE_DIRECT,
					SourceObjectTypeID: "orders", TargetObjectTypeID: "customers",
				}, true, nil)
			models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", test.target).Return(
				publishedObjectType("kn-a", test.target, test.target+"-resource"), true, nil)
			permissions.EXPECT().RequireQueryData(gomock.Any(), []interfaces.PermissionResource{
				{Type: "knowledge_network", ID: "kn-a"},
				{Type: "object_type", ID: "kn-a/" + test.owner},
				{Type: "relation_type", ID: "kn-a/order-customer"},
				{Type: "object_type", ID: "kn-a/" + test.target},
			}).Return(nil)

			err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", &interfaces.ObjectMetricDefinitionV1{
				OwnerObjectTypeID: test.owner,
				Specification: interfaces.ObjectMetricSpecificationV1{
					BusinessQualifiers: &interfaces.ObjectMetricBusinessQualifiersV1{
						RelationPath: &interfaces.ObjectMetricRelationPathV1{Steps: []interfaces.ObjectMetricRelationStepV1{{
							RelationTypeID: "order-customer", Direction: test.direction,
							TargetObjectTypeID: test.target,
						}}},
					},
				},
			}, nil)
			if err != nil {
				t.Fatalf("AuthorizeObjectMetricTrial() direction=%q error = %v", test.direction, err)
			}
		})
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
	var httpErr *httperrors.HTTPError
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
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("AuthorizeObjectMetricTrial() error = %v, want 403", err)
	}
}

func TestAuthorizeObjectMetricTrialRejectsNonFullParentQueryProperties(t *testing.T) {
	for _, test := range []struct {
		name          string
		parentQuery   interfaces.ObjectMetricQueryRequestV1
		topLevelQuery *interfaces.ObjectMetricQueryRequestV1
		allow         bool
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
			name: "top-level order dimension",
			topLevelQuery: &interfaces.ObjectMetricQueryRequestV1{OrderBy: []interfaces.ObjectMetricOrderByV1{{
				Field: "dimension", Direction: "asc", Dimension: &interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "orders", PropertyName: "status"},
			}}},
		},
		{
			name: "parent order dimension",
			parentQuery: interfaces.ObjectMetricQueryRequestV1{OrderBy: []interfaces.ObjectMetricOrderByV1{{
				Field: "dimension", Direction: "asc", Dimension: &interfaces.ObjectMetricPropertyRefV1{ObjectTypeID: "orders", PropertyName: "status"},
			}}},
		},
		{
			name:          "value order needs no additional property",
			topLevelQuery: &interfaces.ObjectMetricQueryRequestV1{OrderBy: []interfaces.ObjectMetricOrderByV1{{Field: "value", Direction: "asc"}}},
			allow:         true,
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
			decisions := []interfaces.PropertyAccessDecision{
				{Name: "amount", Level: interfaces.PropertyAccessFull},
				{Name: "status", Level: interfaces.PropertyAccessSchema},
			}
			properties.EXPECT().ResolvePropertyLevels(gomock.Any(), []interfaces.PropertyLevelsRequestItem{
				{ObjectTypeRef: "kn-a/orders", Properties: []string{"amount", "status"}},
			}).Return([]interfaces.PropertyLevelsDecisionEntry{{
				ObjectTypeRef: "kn-a/orders",
				Properties:    decisions,
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
			if test.topLevelQuery != nil {
				query = test.topLevelQuery
			}
			err := service.AuthorizeObjectMetricTrial(context.Background(), "kn-a", "main", definition, query)
			if test.allow {
				if err != nil {
					t.Fatalf("ordering by value should not require property access: %v", err)
				}
				return
			}
			var httpErr *httperrors.HTTPError
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
	var httpErr *httperrors.HTTPError
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
			var httpErr *httperrors.HTTPError
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
		httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden))

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
			httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)))

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
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if httpErr.HTTPCode != expected {
		t.Fatalf("HTTP status = %d, want %d", httpErr.HTTPCode, expected)
	}
}

type allowMetricSnapshotProxies struct{ err error }

func (service allowMetricSnapshotProxies) AuthorizeMetricSnapshotProxies(context.Context, []*interfaces.TrustedProxyContext) error {
	return service.err
}

func TestMetricFingerprintRejectsLiveProxyRevocation(t *testing.T) {
	models := omock.NewMockOntologyManagerAccess(gomock.NewController(t))
	models.EXPECT().GetObjectType(gomock.Any(), "kn-a", "main", "orders").AnyTimes().Return(publishedObjectType("kn-a", "orders", "resource"), true, nil)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "user", Type: "user"})
	service := &queryAuthorizationService{models: models, proxy: fixedMetricProxy{version: 1}, rowFilters: fixedMetricRowFilters{digest: "rows"}, snapshotProxies: allowMetricSnapshotProxies{err: httperrors.NewHTTPError(ctx, 403, httperrors.PublicError_Forbidden)}}
	fingerprint, err := service.objectMetricAuthorizationFingerprint(ctx, "kn-a", []objectMetricAuthorizationInput{{definition: &interfaces.ObjectMetricDefinitionV1{OwnerObjectTypeID: "orders"}}})
	assertQueryAuthHTTPStatus(t, err, 403)
	if fingerprint.Digest != "" {
		t.Fatal("revoked proxy received an authorization fingerprint")
	}
}
