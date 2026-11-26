// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func handlingAction() interfaces.ActionType {
	return interfaces.ActionType{
		ATID: "refund",
		Parameters: []interfaces.Parameter{
			{Name: "order_id", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP, Value: "id"},
			{Name: "reason", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT},
			{Name: "amount", ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT},
		},
	}
}

func TestABoundInputReadsAConstantOrAProperty(t *testing.T) {
	action := handlingAction()
	shared := action.Parameters
	err := bindEventInputs(&action, map[string]interfaces.ActionInputBinding{
		"reason": {ValueFrom: "const", Value: "late"},
		"amount": {ValueFrom: "property", Value: " total "},
	})
	if err != nil {
		t.Fatal(err)
	}
	reason, amount := action.Parameters[1], action.Parameters[2]
	if reason.ValueFrom != "const" || reason.Value != "late" || amount.ValueFrom != "property" || amount.Value != "total" {
		t.Fatalf("bound %+v %+v", reason, amount)
	}
	if shared[1].ValueFrom != "input" || shared[2].ValueFrom != "input" {
		t.Fatalf("the shared definition changed: %+v", shared)
	}
	execution := &actionSchedulerService{}
	params, err := execution.buildExecutionParams(&action, map[string]any{"id": "PO-1", "total": 12.5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if params["reason"] != "late" || params["amount"] != 12.5 || params["order_id"] != "PO-1" {
		t.Fatalf("params %v", params)
	}
}

func TestABindingMustNameAnInputAndSayWhereItsValueIsFrom(t *testing.T) {
	cases := map[string]struct {
		bindings map[string]interfaces.ActionInputBinding
		want     string
	}{
		"not an input":   {map[string]interfaces.ActionInputBinding{"order_id": {ValueFrom: "const", Value: "x"}}, "not an input"},
		"no such input":  {map[string]interfaces.ActionInputBinding{"note": {ValueFrom: "const", Value: "x"}}, "no input note"},
		"unknown source": {map[string]interfaces.ActionInputBinding{"reason": {ValueFrom: "input", Value: "x"}}, "value_from"},
		"no property":    {map[string]interfaces.ActionInputBinding{"reason": {ValueFrom: "property", Value: " "}}, "without its name"},
		"no constant":    {map[string]interfaces.ActionInputBinding{"reason": {ValueFrom: "const"}}, "without a value"},
	}
	for name, tc := range cases {
		action := handlingAction()
		err := bindEventInputs(&action, tc.bindings)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
		if action.Parameters[1].ValueFrom != "input" {
			t.Errorf("%s: a refused binding changed the action", name)
		}
	}
}
