// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"fmt"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

// ValidateFulltextConditions resources in the table there is no local index declined to full-text search conditions.
//
// Once a table resource has created a local index, it will automatically switch to an index query (see the LocalIndexName branch in Query).
// Operators like match are thus available; If no index is created, it will fall back into the SQL of the connector. There is no full-text capability on that side.
// If you don't block it here, the request will go all the way to the default branch of the SQL generator and then explode, reporting "operator not.
// Distinguish unsupported capability from an index that has not been built; callers handle these cases differently.
// The former needs to modify the query, while the latter needs to build an index.
//
// Vector retrieval does not go here: resolveVectorConditions has already provided more precise judgments in the field parsing stage
// (Whether a certain field has vector characteristics or not), this function only fills in half of the full text.
func validateFulltextConditions(resource *interfaces.Resource, cfg *interfaces.FilterCondCfg) error {
	if resource == nil || cfg == nil {
		return nil
	}
	// The index category and the dataset themselves are stored by OpenSearch and do not rely on the local index produced by the build task.
	if resource.Category != interfaces.ResourceCategoryTable || resource.LocalIndexName != "" {
		return nil
	}
	return rejectFulltext(resource, cfg)
}

func rejectFulltext(resource *interfaces.Resource, cfg *interfaces.FilterCondCfg) error {
	if cfg == nil {
		return nil
	}
	for _, sub := range cfg.SubConds {
		if err := rejectFulltext(resource, sub); err != nil {
			return err
		}
	}
	if !filter_condition.IsFulltextOperation(cfg.Operation) {
		return nil
	}
	return fmt.Errorf("condition [%s] on field %q: resource %q has no local index; build one before full-text search",
		cfg.Operation, cfg.Name, resource.Name)
}
