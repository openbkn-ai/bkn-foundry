// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See LICENSE-OPENBKN.txt in the project root for details.

package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func TestIsFeatureSupported(t *testing.T) {
	tests := []struct {
		propertyType string
		featureType  string
		want         bool
	}{
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_Integer, interfaces.PropertyFeatureType_Fulltext, false},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Keyword, true},
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_Vector, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_Text, "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.propertyType+"/"+tt.featureType, func(t *testing.T) {
			assert.Equal(t, tt.want, IsFeatureSupported(tt.propertyType, tt.featureType))
		})
	}
}

func TestIsFeatureRefPropertyTypeSupported(t *testing.T) {
	assert.True(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_String, interfaces.PropertyFeatureType_Keyword))
	assert.True(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_Text, interfaces.PropertyFeatureType_Fulltext))
	assert.True(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_Vector, interfaces.PropertyFeatureType_Vector))
	assert.False(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword))
}
