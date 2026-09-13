// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See LICENSE-OPENBKN.txt in the project root for details.

package resource

import "vega-backend/interfaces"

// IsFeatureSupported reports whether a Property type can generate a Feature type.
func IsFeatureSupported(propertyType string, featureType string) bool {
	switch propertyType {
	case interfaces.DataType_String:
		return featureType == interfaces.PropertyFeatureType_Keyword ||
			featureType == interfaces.PropertyFeatureType_Fulltext ||
			featureType == interfaces.PropertyFeatureType_Vector
	case interfaces.DataType_Text:
		return featureType == interfaces.PropertyFeatureType_Keyword ||
			featureType == interfaces.PropertyFeatureType_Fulltext ||
			featureType == interfaces.PropertyFeatureType_Vector
	case interfaces.DataType_Vector:
		return featureType == interfaces.PropertyFeatureType_Vector
	default:
		return false
	}
}

// NormalizeSelfReferencingFeatures removes ref_property values that point to their own property.
//
// ref_property means that a feature attached to property A acts on field B. Pointing it back to A
// is redundant: capability derivation already falls back to the property itself when ref_property
// is empty (see VegaResourceIndexCaps in bkn-backend), so both forms are equivalent.
//
// The platform historically persisted legacy resources in this form, so normalize them instead of
// rejecting them. Apply normalization to both request and stored schemas; normalizing only one side
// makes their Features unequal, causing validateMutableSchemaUpdate to classify an ordinary edit as
// a build-related change, clear LocalIndexName, and invalidate an existing index.
func NormalizeSelfReferencingFeatures(props []*interfaces.Property) {
	for _, prop := range props {
		if prop == nil {
			continue
		}
		for i := range prop.Features {
			if prop.Features[i].RefProperty == prop.Name {
				prop.Features[i].RefProperty = ""
			}
		}
	}
}

// AddDefaultTextKeywordFeatures upgrades a newly submitted resource schema so text fields retain
// exact-comparison capability when a table is queried through its local index. Persisting the
// feature is intentional: build and query code can then distinguish a re-saved resource from a
// legacy resource whose existing index does not contain the keyword subfield.
func AddDefaultTextKeywordFeatures(props []*interfaces.Property) {
	for _, prop := range props {
		if prop == nil || prop.Type != interfaces.DataType_Text || hasKeywordFeature(prop.Features) {
			continue
		}
		prop.Features = append(prop.Features, interfaces.PropertyFeature{
			FeatureName: interfaces.LocalIndexKeywordSubfieldName,
			FeatureType: interfaces.PropertyFeatureType_Keyword,
			IsDefault:   true,
			Config: map[string]any{
				"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove,
			},
		})
	}
}

// TextFieldsWithoutKeyword returns text fields that still use the legacy schema contract.
func TextFieldsWithoutKeyword(props []*interfaces.Property) []string {
	var fields []string
	for _, prop := range props {
		if prop != nil && prop.Type == interfaces.DataType_Text && !hasKeywordFeature(prop.Features) {
			fields = append(fields, prop.Name)
		}
	}
	return fields
}

func hasKeywordFeature(features []interfaces.PropertyFeature) bool {
	for _, feature := range features {
		if feature.FeatureType == interfaces.PropertyFeatureType_Keyword {
			return true
		}
	}
	return false
}

// IsFeatureRefPropertyTypeSupported reports whether a referenced result Property
// has the type produced by a Feature.
func IsFeatureRefPropertyTypeSupported(propertyType string, featureType string) bool {
	switch featureType {
	case interfaces.PropertyFeatureType_Keyword:
		return propertyType == interfaces.DataType_String
	case interfaces.PropertyFeatureType_Fulltext:
		return propertyType == interfaces.DataType_Text
	case interfaces.PropertyFeatureType_Vector:
		return propertyType == interfaces.DataType_Vector
	default:
		return false
	}
}
