// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func relationFixture() []*interfaces.RelationType {
	return []*interfaces.RelationType{
		{
			ID: "po2sup", Name: "purchase order to supplier",
			Comment:            "which supplier a purchase order was placed with",
			SourceObjectTypeID: "po", TargetObjectTypeID: "supplier",
		},
		{
			ID: "so2prod", Name: "sales order to product",
			SourceObjectTypeID: "so", TargetObjectTypeID: "product",
		},
	}
}

// Two object type ids do not say what joining them means, and this response is where a caller
// decides which relations to walk. The brief payload is the MCP default, so dropping the comment
// there left relation semantics reachable only through another call.
func TestBriefKeepsTheRelationComment(t *testing.T) {
	svc := &localSearchImpl{}

	for _, brief := range []bool{true, false} {
		converted := svc.convertRelationTypesToLocal(relationFixture(), brief)

		if converted[0].Comment != "which supplier a purchase order was placed with" {
			t.Errorf("brief=%v lost the relation comment: %+v", brief, converted[0])
		}
		if converted[1].Comment != "" {
			t.Errorf("brief=%v invented a comment: %+v", brief, converted[1])
		}
	}
}

// concept_type is what brief still drops: the array the relation sits in already says it.
func TestBriefStillDropsConceptType(t *testing.T) {
	svc := &localSearchImpl{}

	brief := svc.convertRelationTypesToLocal(relationFixture(), true)
	full := svc.convertRelationTypesToLocal(relationFixture(), false)

	if brief[0].ConceptType != "" {
		t.Errorf("brief published concept_type: %+v", brief[0])
	}
	if full[0].ConceptType != "relation_type" {
		t.Errorf("the full payload lost concept_type: %+v", full[0])
	}
}
