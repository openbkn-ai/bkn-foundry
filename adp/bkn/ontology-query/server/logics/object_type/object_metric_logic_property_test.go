// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func TestBuildObjectMetricQueryFromLogicPropertyUsesCompleteObjectIdentity(t *testing.T) {
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "order", PrimaryKeys: []string{"tenant_id", "order_id"},
	}}
	query, err := buildObjectMetricQueryFromLogicProperty(objectType, []interfaces.Filter{
		{Name: "tenant_id", Operation: "==", Value: "t1"},
		{Name: "order_id", Operation: "==", Value: "o1"},
	}, interfaces.MetricPropertyDynamicParams{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.InstanceIdentities) != 1 || query.InstanceIdentities[0]["tenant_id"] != "t1" || query.InstanceIdentities[0]["order_id"] != "o1" {
		t.Fatalf("unexpected instance identity: %#v", query.InstanceIdentities)
	}
}

func TestBuildObjectMetricQueryFromLogicPropertyRejectsPartialIdentity(t *testing.T) {
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "order", PrimaryKeys: []string{"tenant_id", "order_id"},
	}}
	_, err := buildObjectMetricQueryFromLogicProperty(objectType, []interfaces.Filter{
		{Name: "order_id", Operation: "==", Value: "o1"},
	}, interfaces.MetricPropertyDynamicParams{}, nil, nil)
	if err == nil {
		t.Fatal("partial compound identity must be rejected")
	}
}
