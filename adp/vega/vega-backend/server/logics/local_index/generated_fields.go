// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package local_index

import "vega-backend/interfaces"

// GeneratedFields derives physical fields generated for a local index.
func GeneratedFields(schemaDefinition []*interfaces.Property) map[string]*interfaces.Property {
	generated := map[string]*interfaces.Property{}
	for _, property := range schemaDefinition {
		if property == nil {
			continue
		}
		for _, feature := range property.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			fieldName := property.Name
			if feature.RefProperty != "" {
				fieldName = feature.RefProperty
			}
			generatedName := VectorFieldName(fieldName)
			generated[generatedName] = &interfaces.Property{
				Name: generatedName,
				Type: interfaces.DataType_Vector,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      feature.Config,
				}},
			}
		}
	}
	if len(generated) == 0 {
		return nil
	}
	return generated
}
