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
