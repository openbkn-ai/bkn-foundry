// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// filterable is projected from the bound resource while responding (#3365). A
// client that saves back the detail it read sends it too; the definition must
// not keep it, or the views that read the stored definition repeat a value the
// resource may have changed since.
func TestObjectTypeInsertValuesDropsFilterable(t *testing.T) {
	off := false
	objectType := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		DataProperties: []*interfaces.DataProperty{
			{Name: "claim_value", Type: "json", Filterable: &off},
			{Name: "claim_code", Type: "string"},
			nil,
		},
	}}

	values, err := objectTypeInsertValues(objectType)
	if err != nil {
		t.Fatalf("objectTypeInsertValues: %v", err)
	}
	var stored string
	for _, value := range values {
		if bytes, ok := value.([]byte); ok && strings.Contains(string(bytes), "claim_value") {
			stored = string(bytes)
		}
	}
	if stored == "" {
		t.Fatalf("no stored data properties among %v", values)
	}
	if strings.Contains(stored, "filterable") {
		t.Errorf("stored data properties = %s, want no filterable", stored)
	}
	if objectType.DataProperties[0].Filterable == nil {
		t.Error("the caller's property lost its filterable")
	}
	if storedDataProperties(nil) != nil {
		t.Error("an absent property list became an empty one")
	}
}
