// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package local_index

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestGeneratedFields(t *testing.T) {
	generated := GeneratedFields([]*interfaces.Property{
		{Name: "stadium_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}},
		{Name: "description", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
			FeatureType: interfaces.PropertyFeatureType_Vector,
			RefProperty: "summary",
		}}},
		{Name: "embedding", Type: interfaces.DataType_Vector, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}},
		{Name: "city_name", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}}},
	})

	require.Len(t, generated, 1)
	assert.Equal(t, interfaces.DataType_Vector, generated["stadium_name_vector"].Type)
	assert.NotContains(t, generated, "summary_vector")
	assert.NotContains(t, generated, "embedding_vector")
}
