// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import "fmt"

// convertNotExistCondToDatasetFilterCondition converts a not_exist condition to the dataset
// filter format. It matches the documents that do not carry the field at all.
func convertNotExistCondToDatasetFilterCondition(cfg *CondCfg) (map[string]any, error) {
	if cfg.Field == "" {
		return nil, fmt.Errorf("condition [not_exist] field is empty")
	}
	return map[string]any{
		"field":     cfg.Field,
		"operation": "not_exist",
	}, nil
}
