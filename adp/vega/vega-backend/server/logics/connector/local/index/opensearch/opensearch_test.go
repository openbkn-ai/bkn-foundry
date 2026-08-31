// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestCreateDocumentsSplitsBulkRequestsBySerializedSize(t *testing.T) {
	var requestSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requestSizes = append(requestSizes, len(body))
		writeBulkSuccess(t, w, body)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	document := map[string]any{"_id": "doc-1", "content": string(make([]byte, 128))}
	encoded, err := encodeBulkDocument("index-1", document, defaultBulkRequestMaxBytes)
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client, bulkRequestMaxBytes: len(encoded) + 1}

	ids, err := connector.CreateDocuments(context.Background(), "index-1", []map[string]any{
		document,
		{"_id": "doc-2", "content": string(make([]byte, 128))},
		{"_id": "doc-3", "content": string(make([]byte, 128))},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"doc-1", "doc-2", "doc-3"}, ids)
	assert.Equal(t, []int{len(encoded), len(encoded), len(encoded)}, requestSizes)
	assert.Equal(t, "doc-1", document["_id"], "bulk encoding must not mutate the caller's document")
}

func TestEncodeBulkDocumentRejectsSingleDocumentOverByteLimit(t *testing.T) {
	document := map[string]any{"_id": "doc-1", "content": string(make([]byte, 128))}

	_, err := encodeBulkDocument("index-1", document, 10)

	require.ErrorContains(t, err, "exceeding the 10 byte request limit")
}

func TestSplitBulkDocumentsByBytes(t *testing.T) {
	t.Run("keeps the first document when it already exceeds half", func(t *testing.T) {
		assert.Equal(t, 0, splitBulkDocumentsByBytes([][]byte{
			make([]byte, 14), make([]byte, 6), make([]byte, 6),
		}))
	})

	t.Run("keeps the next document in the right chunk when it exceeds half", func(t *testing.T) {
		assert.Equal(t, 0, splitBulkDocumentsByBytes([][]byte{
			make([]byte, 4), make([]byte, 12), make([]byte, 12),
		}))
	})

	t.Run("keeps the right chunk non-empty", func(t *testing.T) {
		assert.Equal(t, 0, splitBulkDocumentsByBytes([][]byte{
			make([]byte, 4), make([]byte, 12),
		}))
	})
}

func TestCreateDocumentsSplitsRejectedLargeRequests(t *testing.T) {
	for _, statusCode := range []int{http.StatusRequestEntityTooLarge, http.StatusTooManyRequests} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			requestDocumentCounts := []int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				count := len(splitBulkLines(t, body)) / 2
				requestDocumentCounts = append(requestDocumentCounts, count)
				if count > 3 {
					w.WriteHeader(statusCode)
					if statusCode == http.StatusTooManyRequests {
						_, err = w.Write([]byte(`{"error":{"type":"rejected_execution_exception"}}`))
					} else {
						_, err = w.Write([]byte(`{"error":"request entity too large"}`))
					}
					require.NoError(t, err)
					return
				}
				writeBulkSuccess(t, w, body)
			}))
			t.Cleanup(server.Close)

			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			connector := &OpenSearchConnector{client: client, bulkRequestMaxBytes: 1024 * 1024}

			ids, err := connector.CreateDocuments(context.Background(), "index-1", []map[string]any{
				{"_id": "doc-1", "content": "same"},
				{"_id": "doc-2", "content": "same"},
				{"_id": "doc-3", "content": "same"},
				{"_id": "doc-4", "content": "same"},
			})

			require.NoError(t, err)
			assert.Equal(t, []int{4, 2, 2}, requestDocumentCounts)
			assert.ElementsMatch(t, []string{"doc-1", "doc-2", "doc-3", "doc-4"}, ids)
		})
	}
}

func TestCreateDocumentsStopsAfterOneRejectedSplit(t *testing.T) {
	requestDocumentCounts := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requestDocumentCounts = append(requestDocumentCounts, len(splitBulkLines(t, body))/2)
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, err = w.Write([]byte(`{"error":"request entity too large"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client, bulkRequestMaxBytes: 1024 * 1024}

	_, err = connector.CreateDocuments(context.Background(), "index-1", []map[string]any{
		{"_id": "doc-1", "content": "same"},
		{"_id": "doc-2", "content": "same"},
		{"_id": "doc-3", "content": "same"},
		{"_id": "doc-4", "content": "same"},
	})

	require.Error(t, err)
	assert.Equal(t, []int{4, 2}, requestDocumentCounts)
}

func splitBulkLines(t *testing.T, body []byte) [][]byte {
	lines := bytesSplit(body, '\n')
	require.Empty(t, lines[len(lines)-1])
	return lines[:len(lines)-1]
}

func writeBulkSuccess(t *testing.T, w http.ResponseWriter, body []byte) {
	items := make([]map[string]map[string]string, 0)
	lines := splitBulkLines(t, body)
	for i := 0; i < len(lines); i += 2 {
		var metadata map[string]map[string]string
		require.NoError(t, json.Unmarshal(lines[i], &metadata))
		items = append(items, map[string]map[string]string{"index": {"_id": metadata["index"]["_id"]}})
	}
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"errors": false, "items": items}))
}

func bytesSplit(value []byte, separator byte) [][]byte {
	result := make([][]byte, 0)
	start := 0
	for index, current := range value {
		if current == separator {
			result = append(result, value[start:index])
			start = index + 1
		}
	}
	return append(result, value[start:])
}

func TestIndexDocumentsRequiresDocumentID(t *testing.T) {
	c := &OpenSearchConnector{}

	_, err := c.IndexDocuments(context.Background(), "index-1", map[string]map[string]any{
		"": {"title": "document without ID"},
	})

	require.ErrorContains(t, err, "id is required")
}

func TestBuildFieldMappingsStringFulltextAddsTextSubfield(t *testing.T) {
	t.Run("string fulltext creates text subfield", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "team_name",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{
					{
						FeatureName: "fulltext",
						FeatureType: interfaces.PropertyFeatureType_Fulltext,
						Config:      map[string]any{"analyzer": "ik_max_word"},
					},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field, _ := props["team_name"].(map[string]any)
		assert.Equal(t, "keyword", field["type"])
		fields, ok := field["fields"].(map[string]any)
		require.True(t, ok)
		sub, ok := fields["fulltext"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "text", sub["type"])
		assert.Equal(t, "ik_max_word", sub["analyzer"])
	})
}

func TestBuildFieldMappingsStringFulltextNoConfig(t *testing.T) {
	t.Run("string fulltext without config uses default analyzer", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "title",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field := props["title"].(map[string]any)
		sub := field["fields"].(map[string]any)["fulltext"].(map[string]any)
		assert.Equal(t, "text", sub["type"])
		assert.NotContains(t, sub, "analyzer")
	})
}

func TestBuildFieldMappingsStringKeywordAndFulltext(t *testing.T) {
	t.Run("string keyword and fulltext keeps keyword config and text subfield", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "name",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}},
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field := props["name"].(map[string]any)
		assert.Equal(t, "keyword", field["type"])
		assert.Equal(t, 256, field["ignore_above"])
		sub := field["fields"].(map[string]any)["fulltext"].(map[string]any)
		assert.Equal(t, "text", sub["type"])
		assert.Equal(t, "standard", sub["analyzer"])
	})
}

func TestBuildFieldMappingsTextFulltextSetsAnalyzer(t *testing.T) {
	t.Run("text fulltext sets analyzer on main field", func(t *testing.T) {
		c := &OpenSearchConnector{}
		schema := []*interfaces.Property{
			{
				Name: "body",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "hanlp_index"}},
				},
			},
		}

		props, _, err := c.buildFieldMappings(schema)

		require.NoError(t, err)
		field := props["body"].(map[string]any)
		assert.Equal(t, "text", field["type"])
		assert.Equal(t, "hanlp_index", field["analyzer"])
	})
}
