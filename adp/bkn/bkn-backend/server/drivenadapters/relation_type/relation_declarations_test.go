// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package relation_type

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A relation type without declarations writes NULL in every declaration column, the way rows saved
// before migration 0.2.4 read; one with declarations reads back what it wrote.
func TestRelationDeclarationColumnsRoundTrip(t *testing.T) {
	ctx := context.Background()
	values, err := declarationValues(&interfaces.RelationType{})
	require.NoError(t, err)
	require.Len(t, values, len(declarationColumns))
	for i, v := range values {
		assert.Nil(t, v, declarationColumns[i])
	}
	var none scannedDeclarations
	read := &interfaces.RelationType{}
	none.apply(ctx, read)
	assert.Equal(t, interfaces.RelationDeclarations{}, read.RelationDeclarations)

	written := interfaces.RelationDeclarations{
		ReverseName: "雇员", Aliases: []string{"在职"}, Cardinality: "many_to_one",
		Constraints: &interfaces.RelationConstraints{Required: []string{"source"}},
		Lifecycle:   &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "改用任职记录", ReplacedBy: "employment"},
		Importance:  "secondary", FallbackFor: "works_in_direct", Verification: "unconfirmed",
	}
	values, err = declarationValues(&interfaces.RelationType{RelationDeclarations: written})
	require.NoError(t, err)
	column := func(v any) []byte {
		if v == nil {
			return nil
		}
		return []byte(v.(string))
	}
	scanned := scannedDeclarations{
		reverseName: column(values[0]), aliases: column(values[1]), cardinality: column(values[2]),
		constraints: column(values[3]), lifecycleStatus: column(values[4]), deprecation: column(values[5]),
		importance: column(values[6]), fallbackFor: column(values[7]), verification: column(values[8]),
	}
	read = &interfaces.RelationType{}
	scanned.apply(ctx, read)
	assert.Equal(t, written, read.RelationDeclarations)

	scanned.constraints = []byte("{not json")
	scanned.apply(ctx, read)
	assert.Nil(t, read.Constraints, "a column that does not decode reads as none")
	assert.Equal(t, "雇员", read.ReverseName, "and the rest still reads")
}
