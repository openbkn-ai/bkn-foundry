// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package local_index

import "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"

// GeneratedFields derives physical fields generated for a local index.
func (lim *localIndexManager) GeneratedFields(schemaDefinition []*interfaces.Property) (map[string]*interfaces.Property, error) {
	mappings, err := queryFieldMappings(schemaDefinition)
	if err != nil {
		return nil, err
	}
	generated := map[string]*interfaces.Property{}
	for _, property := range schemaDefinition {
		if property.Type != interfaces.DataType_String && property.Type != interfaces.DataType_Text {
			continue
		}
		for _, feature := range property.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector || feature.RefProperty != "" {
				continue
			}
			generatedName := VectorFieldName(property.Name)
			mapping, _ := mappings[generatedName].(map[string]any)
			filterable := lim.lic.IsFilterable(indexFieldMeta(generatedName, mapping))
			generated[generatedName] = &interfaces.Property{
				Name:       generatedName,
				Type:       interfaces.DataType_Vector,
				Filterable: &filterable,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      feature.Config,
				}},
			}
		}
	}
	return generated, nil
}
