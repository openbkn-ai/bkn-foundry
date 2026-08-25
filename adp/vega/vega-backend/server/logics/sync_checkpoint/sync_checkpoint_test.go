// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sync_checkpoint

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestEncodeBatch(t *testing.T) {
	mark, err := EncodeBatch(nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":1,"mode":"batch","cursor":[]}`, mark)

	checkpoint, err := DecodeBatch(mark)
	require.NoError(t, err)
	require.NotNil(t, checkpoint)
	assert.Empty(t, checkpoint.Cursor)
}

func TestDecodeBatch(t *testing.T) {
	t.Run("empty mark means no checkpoint", func(t *testing.T) {
		checkpoint, err := DecodeBatch("")
		require.NoError(t, err)
		assert.Nil(t, checkpoint)
	})

	t.Run("preserves JSON numbers", func(t *testing.T) {
		checkpoint, err := DecodeBatch(`{"version":1,"mode":"batch","cursor":[{"key":"id","value":9223372036854775807}]}`)
		require.NoError(t, err)
		require.Len(t, checkpoint.Cursor, 1)
		value, ok := checkpoint.Cursor[0].Value.(json.Number)
		require.True(t, ok, "cursor number type = %T", checkpoint.Cursor[0].Value)
		assert.Equal(t, "9223372036854775807", value.String())
	})

	for name, mark := range map[string]string{
		"whitespace is not an empty mark": "   ",
		"legacy V0 array":                 `[]`,
		"missing cursor":                  `{"version":1,"mode":"batch"}`,
		"null cursor":                     `{"version":1,"mode":"batch","cursor":null}`,
		"wrong version":                   `{"version":2,"mode":"batch","cursor":[]}`,
		"wrong mode":                      `{"version":1,"mode":"streaming","cursor":[]}`,
		"trailing document":               `{"version":1,"mode":"batch","cursor":[]} {}`,
		"unknown field":                   `{"version":1,"mode":"batch","cursor":[],"extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			checkpoint, err := DecodeBatch(mark)
			assert.Error(t, err)
			assert.Nil(t, checkpoint)
		})
	}
}

func TestValidateCursor(t *testing.T) {
	schema := []*interfaces.Property{
		{Name: "signed_id", Type: interfaces.DataType_Integer},
		{Name: "unsigned_id", Type: interfaces.DataType_UnsignedInteger},
		{Name: "created_at", Type: interfaces.DataType_Timestamp},
	}
	checkpoint, err := DecodeBatch(`{"version":1,"mode":"batch","cursor":[{"key":"signed_id","value":9223372036854775807},{"key":"unsigned_id","value":18446744073709551615},{"key":"created_at","value":"2026-08-25T08:00:00Z"}]}`)
	require.NoError(t, err)

	require.NoError(t, ValidateCursor(checkpoint, []string{"signed_id", "unsigned_id", "created_at"}, schema))
	assert.Equal(t, int64(math.MaxInt64), checkpoint.Cursor[0].Value)
	assert.Equal(t, uint64(math.MaxUint64), checkpoint.Cursor[1].Value)
	assert.Equal(t, "2026-08-25T08:00:00Z", checkpoint.Cursor[2].Value)

	tests := []struct {
		name      string
		mark      string
		buildKeys []string
		schema    []*interfaces.Property
	}{
		{
			name:      "cursor key order differs",
			mark:      `{"version":1,"mode":"batch","cursor":[{"key":"unsigned_id","value":1},{"key":"signed_id","value":1}]}`,
			buildKeys: []string{"signed_id", "unsigned_id"},
			schema:    schema,
		},
		{
			name:      "cursor count differs",
			mark:      `{"version":1,"mode":"batch","cursor":[{"key":"signed_id","value":1}]}`,
			buildKeys: []string{"signed_id", "unsigned_id"},
			schema:    schema,
		},
		{
			name:      "integer is fractional",
			mark:      `{"version":1,"mode":"batch","cursor":[{"key":"signed_id","value":1.5}]}`,
			buildKeys: []string{"signed_id"},
			schema:    schema,
		},
		{
			name:      "string cursor is numeric",
			mark:      `{"version":1,"mode":"batch","cursor":[{"key":"created_at","value":1}]}`,
			buildKeys: []string{"created_at"},
			schema:    schema,
		},
		{
			name:      "build key is absent from schema",
			mark:      `{"version":1,"mode":"batch","cursor":[{"key":"missing","value":"x"}]}`,
			buildKeys: []string{"missing"},
			schema:    schema,
		},
		{
			name:      "build key type is unsupported",
			mark:      `{"version":1,"mode":"batch","cursor":[{"key":"score","value":1}]}`,
			buildKeys: []string{"score"},
			schema:    []*interfaces.Property{{Name: "score", Type: interfaces.DataType_Float}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkpoint, err := DecodeBatch(tt.mark)
			require.NoError(t, err)
			assert.Error(t, ValidateCursor(checkpoint, tt.buildKeys, tt.schema))
		})
	}

	t.Run("empty cursor is a valid established baseline", func(t *testing.T) {
		checkpoint, err := DecodeBatch(`{"version":1,"mode":"batch","cursor":[]}`)
		require.NoError(t, err)
		require.NoError(t, ValidateCursor(checkpoint, []string{"signed_id"}, schema))
	})
}
