// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"testing"

	"bkn-backend/interfaces"
)

func TestRejectClientSpecifiedChildIDs(t *testing.T) {
	ctx := context.Background()
	if err := rejectClientSpecifiedChildIDs(ctx, interfaces.ImportMode_Normal, "client-id"); err == nil {
		t.Fatal("normal creation must reject a client-specified child ID")
	}
	if err := rejectClientSpecifiedChildIDs(ctx, interfaces.ImportMode_Normal, ""); err != nil {
		t.Fatalf("normal creation with an empty ID returned %v", err)
	}
	if err := rejectClientSpecifiedChildIDs(ctx, interfaces.ImportMode_Overwrite, "import-id"); err != nil {
		t.Fatalf("import conflict modes must retain existing ID handling: %v", err)
	}
}

func TestRejectClientSpecifiedKNChildIDsChecksNestedResources(t *testing.T) {
	kn := &interfaces.KN{ConceptGroups: []*interfaces.ConceptGroup{{
		ObjectTypes: []*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "nested-id"},
		}},
	}}}
	if err := rejectClientSpecifiedKNChildIDs(context.Background(), interfaces.ImportMode_Normal, kn); err == nil {
		t.Fatal("normal knowledge-network creation must reject nested client-specified child IDs")
	}
}
