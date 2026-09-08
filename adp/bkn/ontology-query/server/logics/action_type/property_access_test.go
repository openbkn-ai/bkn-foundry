// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"reflect"
	"testing"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
)

func TestActionPropertyDependenciesRequireOnlyDataInputsToBeFull(t *testing.T) {
	actionType := interfaces.ActionType{
		Condition: &cond.CondCfg{Name: "status", Operation: cond.OperationEq},
		Parameters: []interfaces.Parameter{
			{Name: "body.id", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "id"},
			{Name: "body.risk", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "risk_score"},
			{Name: "body.legacy", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "deleted_property"},
		},
	}
	objectType := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		PrimaryKeys:     []string{"id"},
		DataProperties:  []cond.DataProperty{{Name: "id"}, {Name: "status"}},
		LogicProperties: []*interfaces.LogicProperty{{Name: "risk_score"}},
	}}

	returned, full := actionPropertyDependencies(actionType, objectType)
	if !reflect.DeepEqual(returned, []string{"id", "risk_score"}) {
		t.Fatalf("action return properties = %#v", returned)
	}
	if !reflect.DeepEqual(full, []string{"id", "status"}) {
		t.Fatalf("action full dependencies = %#v", full)
	}
}

func TestActionWithoutPropertyParametersFetchesOnlyPrimaryKeys(t *testing.T) {
	returned, full := actionPropertyDependencies(interfaces.ActionType{}, interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{PrimaryKeys: []string{"id"}},
	})
	if !reflect.DeepEqual(returned, []string{"id"}) || len(full) != 0 {
		t.Fatalf("action dependencies = %#v, %#v", returned, full)
	}
}
