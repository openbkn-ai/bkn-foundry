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
