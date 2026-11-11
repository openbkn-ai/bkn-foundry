// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"fmt"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
)

// ResolveActionConditionValues produces a literal condition for one endpoint
// without mutating the stored definition or reading another endpoint's data.
func ResolveActionConditionValues(cfg *cond.CondCfg, data, dynamic map[string]any) (*cond.CondCfg, error) {
	if cfg == nil {
		return nil, nil
	}
	result := *cfg
	if cfg.Operation == cond.OperationAnd || cfg.Operation == cond.OperationOr {
		if len(cfg.SubConds) == 0 {
			return nil, fmt.Errorf("logical action conditions require children")
		}
		result.SubConds = make([]*cond.CondCfg, len(cfg.SubConds))
		for index, child := range cfg.SubConds {
			if child == nil {
				return nil, fmt.Errorf("action condition contains a null child")
			}
			value, err := ResolveActionConditionValues(child, data, dynamic)
			if err != nil {
				return nil, err
			}
			result.SubConds[index] = value
		}
		return &result, nil
	}
	switch cfg.ValueFrom {
	case "", "const":
		return &result, nil
	case "property", "input":
		if _, noValue := cond.NotRequiredValueOperationMap[cfg.Operation]; noValue {
			return nil, fmt.Errorf("condition operator %q does not accept a reference", cfg.Operation)
		}
		name, ok := cfg.Value.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("condition reference requires a name")
		}
		var value any
		if cfg.ValueFrom == "property" {
			value = data[name]
		} else {
			value = ActionDynamicParamGetValue(dynamic, name)
		}
		if value == nil {
			return nil, fmt.Errorf("missing action condition %s reference %q", cfg.ValueFrom, name)
		}
		result.Value, result.ValueFrom = value, "const"
	default:
		return nil, fmt.Errorf("unsupported action condition value_from %q", cfg.ValueFrom)
	}
	return &result, nil
}
