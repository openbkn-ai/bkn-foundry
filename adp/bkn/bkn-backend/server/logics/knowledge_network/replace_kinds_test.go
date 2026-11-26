// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// pruneRecorder records what a replace import keeps.
type pruneRecorder struct {
	interfaces.BranchCopier
	keep map[string][]string
}

func (p *pruneRecorder) PruneBranch(_ context.Context, _ *sql.Tx, _, _ string, keep map[string][]string) error {
	p.keep = keep
	return nil
}

// replacedNetwork is a package with an event class, which does not carry the relation the kind
// keeps for it, and a relation of its own.
func replacedNetwork() *interfaces.KN {
	class := resourceObjectType("late_order", "kept-late_order")
	class.Kinds = []string{"kept"}
	class.Event = &interfaces.ObjectTypeEvent{Source: "rule", Subject: "order"}
	return &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{class, resourceObjectType("order", "orders")},
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "order_peer", SourceObjectTypeID: "order", TargetObjectTypeID: "order",
		}}},
	}
}

// A replace import deletes what the package does not carry, but not the relation the platform made
// with the event class in the same import.
func TestReplaceImportKeepsTheRelationsTheKindsKeep(t *testing.T) {
	registerKeptKind(t)
	recorder := &pruneRecorder{}
	before := logics.BC
	logics.BC = recorder
	t.Cleanup(func() { logics.BC = before })

	if err := pruneReplacedDefinitions(context.Background(), nil, replacedNetwork()); err != nil {
		t.Fatal(err)
	}
	relations := slices.Sorted(slices.Values(recorder.keep[interfaces.MODULE_TYPE_RELATION_TYPE]))
	if !slices.Equal(relations, []string{"late_order_subject", "order_peer"}) {
		t.Fatalf("relation types kept %v", relations)
	}
}

// Nor does it delete that relation's concept document.
func TestReplaceImportKeepsTheConceptDocumentsOfTheRelationsTheKindsKeep(t *testing.T) {
	registerKeptKind(t)
	kn := replacedNetwork()
	documents := conceptDocumentIDs(kn, false)
	for _, id := range []string{"late_order_subject", "order_peer"} {
		document := interfaces.GenerateConceptDocuemtnID(kn.KNID, interfaces.MODULE_TYPE_RELATION_TYPE, id, kn.Branch)
		if _, ok := documents[document]; !ok {
			t.Fatalf("the concept document of relation type %s is not kept", id)
		}
	}
}
