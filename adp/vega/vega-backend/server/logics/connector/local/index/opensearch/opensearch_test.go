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
	"vega-backend/logics/filter_condition"
)

func TestDeleteDocumentsByQueryRejectsEmptyFilter(t *testing.T) {
	connector := &OpenSearchConnector{}

	err := connector.DeleteDocumentsByQuery(context.Background(), "dataset-1", nil, nil)

	require.ErrorContains(t, err, "non-empty filter condition")
}

func TestIsUnconditionalDeleteQuery(t *testing.T) {
	assert.True(t, isUnconditionalDeleteQuery(map[string]any{"match_all": map[string]any{}}))
	assert.True(t, isUnconditionalDeleteQuery(map[string]any{"bool": map[string]any{"must": []map[string]any{}}}))
	assert.False(t, isUnconditionalDeleteQuery(map[string]any{"term": map[string]any{"kind": "remove"}}))
}

func TestDeleteDocumentsByQueryUsesTheFilterCondition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/dataset-1/_delete_by_query", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]any{
			"query": map[string]any{
				"term": map[string]any{"kind": "remove"},
			},
		}, body)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"deleted":1}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	schema := []*interfaces.Property{{Name: "kind", Type: interfaces.DataType_String}}
	condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{
		Name:      "kind",
		Operation: filter_condition.OperationEqual2,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "remove",
		},
	}, map[string]*interfaces.Property{"kind": schema[0]})
	require.NoError(t, err)

	connector := &OpenSearchConnector{client: client}
	err = connector.DeleteDocumentsByQuery(context.Background(), "dataset-1", &interfaces.ResourceDataQueryParams{
		ActualFilterCond: condition,
	}, schema)

	require.NoError(t, err)
}

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

func TestGetDocumentReturnsNilForMissingDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/dataset-1/_doc/missing", r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}

	document, err := connector.GetDocument(context.Background(), "dataset-1", "missing")

	require.NoError(t, err)
	assert.Nil(t, document)
}

func TestGetDocumentsUsesMgetAndPreservesMissingPositions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/dataset-1/_mget", r.URL.Path)
		var body struct {
			IDs []string `json:"ids"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"doc-1", "missing", "doc-2"}, body.IDs)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"docs":[{"_id":"doc-1","found":true,"_source":{"title":"one"}},{"_id":"missing","found":false},{"_id":"doc-2","found":true,"_source":{"title":"two"}}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}

	documents, err := connector.GetDocuments(context.Background(), "dataset-1", []string{"doc-1", "missing", "doc-2"})

	require.NoError(t, err)
	assert.Equal(t, []map[string]any{
		{"_id": "doc-1", "title": "one"},
		nil,
		{"_id": "doc-2", "title": "two"},
	}, documents)
}

func TestCreateIndexAlwaysEnablesKNN(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/dataset-1":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/dataset-1":
			var config map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&config))
			indexSettings := config["settings"].(map[string]any)["index"].(map[string]any)
			assert.Equal(t, true, indexSettings["knn"])
			_, err := w.Write([]byte(`{"acknowledged":true}`))
			require.NoError(t, err)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}

	err = connector.CreateIndex(context.Background(), "dataset-1", map[string]any{
		"title": map[string]any{"type": "keyword"},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"HEAD /dataset-1",
		"PUT /dataset-1",
	}, requests)
}

func TestUpdateIndexOnlyUpdatesMapping(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/dataset-1":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/dataset-1/_mapping":
			_, err := w.Write([]byte(`{"acknowledged":true}`))
			require.NoError(t, err)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}

	err = connector.UpdateIndex(context.Background(), "dataset-1", map[string]any{
		"content_vector": map[string]any{"type": "knn_vector", "dimension": 3},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"HEAD /dataset-1",
		"PUT /dataset-1/_mapping",
	}, requests)
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

	t.Run("chooses the cut point closest to half", func(t *testing.T) {
		assert.Equal(t, 1, splitBulkDocumentsByBytes([][]byte{
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

func TestDeleteDocumentsReturnsBulkItemFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"errors":true,"items":[{"delete":{"_id":"doc-1","status":404,"error":{"type":"document_missing_exception","reason":"missing"}}}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}

	err = connector.DeleteDocuments(context.Background(), "index-1", []string{"doc-1"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "document_missing_exception")
}
