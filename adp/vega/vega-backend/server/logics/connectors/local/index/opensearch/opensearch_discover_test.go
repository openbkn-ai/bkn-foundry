package opensearch

import (
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestPropertyUnmarshalJSON(t *testing.T) {
	t.Run("keeps dynamic attributes and nested fields", func(t *testing.T) {
		var prop Property

		err := sonic.Unmarshal([]byte(`{
			"type": "text",
			"analyzer": "ik_max_word",
			"fields": {
				"keyword": {"type": "keyword", "ignore_above": 256}
			},
			"properties": {
				"title": {"type": "keyword"}
			}
		}`), &prop)

		require.NoError(t, err)
		assert.Equal(t, "text", prop.Type)
		assert.Equal(t, "ik_max_word", prop.Attributes["analyzer"])
		require.Contains(t, prop.Fields, "keyword")
		assert.Equal(t, "keyword", prop.Fields["keyword"].Type)
		assert.Equal(t, float64(256), prop.Fields["keyword"].Attributes["ignore_above"])
		require.Contains(t, prop.Properties, "title")
		assert.Equal(t, "keyword", prop.Properties["title"].Type)
	})

	t.Run("returns json error", func(t *testing.T) {
		var prop Property

		err := prop.UnmarshalJSON([]byte(`{`))

		require.Error(t, err)
	})
}

func TestParseProperties(t *testing.T) {
	t.Run("flattens object fields and keeps sub fields", func(t *testing.T) {
		out := map[string]interfaces.IndexFieldMeta{}
		parseProperties("", map[string]Property{
			"description": {
				Type:       "text",
				Attributes: map[string]any{"analyzer": "ik_max_word"},
				Fields: map[string]Property{
					"keyword": {Type: "keyword", Attributes: map[string]any{"ignore_above": 256}},
				},
			},
			"profile": {
				Type: "object",
				Properties: map[string]Property{
					"age": {Type: "integer", Attributes: map[string]any{"doc_values": true}},
				},
			},
			"ignored": {},
		}, out)

		require.Contains(t, out, "description")
		assert.Equal(t, "description", out["description"].Name)
		assert.Equal(t, "text", out["description"].Type)
		assert.True(t, out["description"].Searchable)
		require.Len(t, out["description"].SubFields, 1)
		assert.Equal(t, "keyword", out["description"].SubFields[0].Name)
		assert.Equal(t, "keyword", out["description"].SubFields[0].Type)
		require.Contains(t, out, "profile.age")
		assert.Equal(t, "integer", out["profile.age"].Type)
		assert.NotContains(t, out, "profile")
		assert.NotContains(t, out, "ignored")
	})
}

func TestCollectSubFields(t *testing.T) {
	t.Run("returns nil for empty fields", func(t *testing.T) {
		assert.Nil(t, collectSubFields(Property{}))
	})

	t.Run("sorts sub fields by name", func(t *testing.T) {
		got := collectSubFields(Property{
			Fields: map[string]Property{
				"z": {Type: "keyword"},
				"a": {Type: "text"},
			},
		})

		require.Len(t, got, 2)
		assert.Equal(t, "a", got[0].Name)
		assert.Equal(t, "z", got[1].Name)
	})
}
