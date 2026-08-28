package opensearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestPropertyUnmarshalJSON(t *testing.T) {
	t.Run("keeps dynamic attributes and nested fields", func(t *testing.T) {
		var prop Property

		err := sonic.Unmarshal([]byte(`{
			"type": "text",
			"index": false,
			"analyzer": "ik_max_word",
			"meta": {"description": "商品标题"},
			"fields": {
				"keyword": {"type": "keyword", "ignore_above": 256}
			},
			"properties": {
				"title": {"type": "keyword"}
			}
		}`), &prop)

		require.NoError(t, err)
		assert.Equal(t, "text", prop.Type)
		assert.Equal(t, false, prop.Attributes["index"])
		assert.Equal(t, "ik_max_word", prop.Attributes["analyzer"])
		assert.Equal(t, map[string]any{"description": "商品标题"}, prop.Attributes["meta"])
		out := map[string]interfaces.IndexFieldMeta{}
		parseProperties("", map[string]Property{"title": prop}, out)
		assert.Equal(t, "商品标题", out["title"].Description)
		assert.False(t, out["title"].Searchable)
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
				Type: "text",
				Attributes: map[string]any{
					"analyzer": "ik_max_word",
					"meta":     map[string]any{"description": "商品标题", "owner": "catalog-team"},
				},
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
			"not_searchable": {
				Type:       "keyword",
				Attributes: map[string]any{"index": false},
			},
			"ignored": {},
		}, out)

		require.Contains(t, out, "description")
		assert.Equal(t, "description", out["description"].Name)
		assert.Equal(t, "商品标题", out["description"].Description)
		assert.Equal(t, "text", out["description"].Type)
		assert.True(t, out["description"].Searchable)
		require.Len(t, out["description"].SubFields, 1)
		assert.Equal(t, "keyword", out["description"].SubFields[0].Name)
		assert.Equal(t, "keyword", out["description"].SubFields[0].Type)
		require.Contains(t, out, "profile.age")
		assert.Equal(t, "integer", out["profile.age"].Type)
		assert.False(t, out["not_searchable"].Searchable)
		assert.NotContains(t, out, "profile")
		assert.NotContains(t, out, "ignored")
	})
}

func TestDescriptionFromFieldMeta(t *testing.T) {
	assert.Equal(t, "字段说明", descriptionFromFieldMeta(map[string]any{
		"meta": map[string]any{"description": "字段说明"},
	}))
	assert.Empty(t, descriptionFromFieldMeta(map[string]any{"meta": map[string]any{"description": 1}}))
	assert.Empty(t, descriptionFromFieldMeta(map[string]any{"meta": "invalid"}))
}

func TestIsSearchable(t *testing.T) {
	assert.True(t, isSearchable(nil))
	assert.True(t, isSearchable(map[string]any{"index": true}))
	assert.False(t, isSearchable(map[string]any{"index": false}))
	assert.True(t, isSearchable(map[string]any{"index": "false"}))
}

func TestFetchMappingsExtractsMappingMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/products/_mapping", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
			"products": {
				"mappings": {
					"_meta": {"description": "产品索引", "version": "1.0", "owner": {"team": "search"}},
					"properties": {"title": {"type": "text"}}
				}
			}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}
	index := &interfaces.IndexMeta{Name: "products", Description: "索引描述", MappingMeta: map[string]any{"old": true}}

	require.NoError(t, connector.fetchMappings(context.Background(), index))
	assert.Equal(t, "索引描述", index.Description)
	assert.Equal(t, map[string]any{
		"description": "产品索引",
		"version":     "1.0",
		"owner":       map[string]any{"team": "search"},
	}, index.MappingMeta)
	require.Contains(t, index.Mapping, "title")
}

func TestGetIndexMetaByIdentifierRejectsIndexOutsideConfiguredPattern(t *testing.T) {
	connector := &OpenSearchConnector{
		Config: &opensearchConfig{IndexPattern: "catalog-*"},
	}

	_, err := connector.GetIndexMetaByIdentifier(context.Background(), "system-audit")

	require.Error(t, err)
	assert.ErrorContains(t, err, `index "system-audit" is outside the connector scope`)
}

func TestFetchMappingsDefaultsMissingMappingMetaToEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"products":{"mappings":{"properties":{}}}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}
	index := &interfaces.IndexMeta{Name: "products", Description: "索引描述", MappingMeta: map[string]any{"old": true}}

	require.NoError(t, connector.fetchMappings(context.Background(), index))
	assert.Equal(t, "索引描述", index.Description)
	assert.Empty(t, index.MappingMeta)
}

func TestFetchMappingsReturnsErrorForInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}

	err = connector.fetchMappings(context.Background(), &interfaces.IndexMeta{Name: "products"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to parse mappings")
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
