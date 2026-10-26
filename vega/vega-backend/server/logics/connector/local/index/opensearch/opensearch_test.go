// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestDeleteDocumentsByQueryRejectsEmptyFilter(t *testing.T) {
	connector := &OpenSearchConnector{}

	_, err := connector.DeleteDocumentsByQuery(context.Background(), "dataset-1", nil, nil)

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
		_, err := w.Write([]byte(`{"deleted":1,"version_conflicts":0,"timed_out":false,"failures":[]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	schema := []*interfaces.Property{{Name: "kind", Type: interfaces.DataType_String}}
	condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{
		Name:      "kind",
		Operation: filter_condition.OperationEqual2,
		ValueFrom: interfaces.ValueFrom_Const,
		Value:     "remove",
	}, map[string]*interfaces.Property{"kind": schema[0]})
	require.NoError(t, err)

	connector := &OpenSearchConnector{client: client}
	_, err = connector.DeleteDocumentsByQuery(context.Background(), "dataset-1", &interfaces.ResourceDataQueryParams{
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
	encoded, err := encodeBulkDocument("index-1", "doc-1", map[string]any{"content": document["content"]}, defaultBulkRequestMaxBytes)
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
	assert.NotContains(t, document, "_id")
}

// 原始响应中的数字必须直接写成 JSON 字面量，避免 fixture 提前丢失精度。
const datasetPrecisionSource = `{
	"integers":[9007199254740991,9007199254740992,9007199254740993,-9007199254740993,-9223372036854775808,9223372036854775807],
	"nested":{"items":[9007199254740993,{"value":-9223372036854775808}]},
	"decimal":1.25,"exponent":1.25e-3,"string":"9007199254740993","boolean":true,"null":null
}`

func assertDatasetDocumentPrecision(t *testing.T, document map[string]any, id string) {
	t.Helper()
	expected := map[string]any{
		"_id": id,
		"integers": []any{
			json.Number("9007199254740991"), json.Number("9007199254740992"),
			json.Number("9007199254740993"), json.Number("-9007199254740993"),
			json.Number("-9223372036854775808"), json.Number("9223372036854775807"),
		},
		"nested": map[string]any{"items": []any{
			json.Number("9007199254740993"), map[string]any{"value": json.Number("-9223372036854775808")},
		}},
		"decimal": json.Number("1.25"), "exponent": json.Number("1.25e-3"),
		"string": "9007199254740993", "boolean": true, "null": nil,
	}
	assert.Equal(t, expected, document)

	// 使用生产响应所用的 Sonic 编码器，验证序列化后仍保留数值和类型。
	encoded, err := sonic.Marshal(document)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var roundTrip map[string]any
	require.NoError(t, decoder.Decode(&roundTrip))
	assert.Equal(t, expected, roundTrip)
}

func TestOpenSearchConnectorGetDocument(t *testing.T) {
	t.Run("missing documents", func(t *testing.T) {
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
	})

	t.Run("preserves numeric literals and JSON types", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			assert.Equal(t, "/dataset-1/_doc/doc-1", r.URL.Path)
			_, err := io.WriteString(w, `{"_id":"doc-1","found":true,"_source":`+datasetPrecisionSource+`}`)
			require.NoError(t, err)
		}))
		t.Cleanup(server.Close)
		client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
		require.NoError(t, err)
		connector := &OpenSearchConnector{client: client}
		document, err := connector.GetDocument(context.Background(), "dataset-1", "doc-1")
		require.NoError(t, err)
		assertDatasetDocumentPrecision(t, document, "doc-1")
	})
}

func TestOpenSearchConnectorGetDocuments(t *testing.T) {
	t.Run("missing documents", func(t *testing.T) {
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
	})

	for _, tc := range []struct {
		name string
		ids  []string
		body string
	}{
		{
			name: "single ID preserves numeric literals and JSON types",
			ids:  []string{"doc-1"},
			body: `{"docs":[{"_id":"doc-1","found":true,"_source":` + datasetPrecisionSource + `}]}`,
		},
		{
			name: "multiple IDs preserve precision and missing positions",
			ids:  []string{"doc-1", "missing", "doc-2"},
			body: `{"docs":[{"_id":"doc-1","found":true,"_source":` + datasetPrecisionSource + `},{"_id":"missing","found":false},{"_id":"doc-2","found":true,"_source":` + datasetPrecisionSource + `}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/dataset-1/_mget", r.URL.Path)
				var body struct {
					IDs []string `json:"ids"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Equal(t, tc.ids, body.IDs)
				w.Header().Set("Content-Type", "application/json")
				_, err := io.WriteString(w, tc.body)
				require.NoError(t, err)
			}))
			t.Cleanup(server.Close)
			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			connector := &OpenSearchConnector{client: client}
			documents, err := connector.GetDocuments(context.Background(), "dataset-1", tc.ids)
			require.NoError(t, err)
			require.Len(t, documents, len(tc.ids))
			for i, id := range tc.ids {
				if id == "missing" {
					assert.Nil(t, documents[i])
					continue
				}
				assertDatasetDocumentPrecision(t, documents[i], id)
			}
		})
	}
}

func TestOpenSearchConnectorCreateIndex(t *testing.T) {
	for _, indexName := range []string{"vega-dataset-test", "vega-build-test"} {
		t.Run(indexName, func(t *testing.T) {
			calls := 0
			client, err := opensearch.NewClient(opensearch.Config{Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "/"+indexName, r.URL.Path)
				status, body := http.StatusNotFound, ""
				if r.Method == http.MethodPut {
					var config map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&config))
					assert.Equal(t, map[string]any{"knn": true}, config["settings"].(map[string]any)["index"])
					assert.Equal(t, map[string]any{"resource_id": "resource"}, config["mappings"].(map[string]any)["_meta"])
					status, body = http.StatusOK, `{"acknowledged":true}`
				} else {
					assert.Equal(t, http.MethodHead, r.Method)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			require.NoError(t, err)
			err = (&OpenSearchConnector{client: client}).CreateIndex(context.Background(), indexName, map[string]any{"title": map[string]any{"type": "keyword"}}, map[string]string{"resource_id": "resource"})
			require.NoError(t, err)
			assert.Equal(t, 2, calls)
		})
	}
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
			var body struct {
				Properties map[string]any `json:"properties"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]any{"type": "object"}, body.Properties["legacy_payload"])
			assert.Equal(t, map[string]any{"type": "object", "enabled": false}, body.Properties["new_payload"])
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
		"legacy_payload": map[string]any{"type": "object"},
		"new_payload":    map[string]any{"type": "object", "enabled": false},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"HEAD /dataset-1",
		"PUT /dataset-1/_mapping",
	}, requests)
}

func TestEncodeBulkDocument(t *testing.T) {
	t.Run("rejects oversized document", func(t *testing.T) {
		_, err := encodeBulkDocument("index-1", "doc-1", map[string]any{"content": string(make([]byte, 128))}, 10)
		require.ErrorContains(t, err, "exceeding the 10 byte request limit")
	})
	for _, id := range []string{"doc-1", ""} {
		t.Run("metadata ID "+id, func(t *testing.T) {
			document := map[string]any{"_id": "discarded", "content": "hello"}
			encoded, err := encodeBulkDocument("index-1", id, document, 1024)
			require.NoError(t, err)
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			var metadata map[string]map[string]string
			var body map[string]any
			require.NoError(t, decoder.Decode(&metadata))
			require.NoError(t, decoder.Decode(&body))
			assert.Equal(t, "index-1", metadata["index"]["_index"])
			if id == "" {
				assert.NotContains(t, metadata["index"], "_id")
			} else {
				assert.Equal(t, id, metadata["index"]["_id"])
			}
			assert.Equal(t, map[string]any{"content": "hello"}, body)
			assert.Equal(t, map[string]any{"content": "hello"}, document)
		})
	}
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
	items := make([]map[string]any, 0)
	lines := splitBulkLines(t, body)
	for i := 0; i < len(lines); i += 2 {
		var metadata map[string]map[string]string
		require.NoError(t, json.Unmarshal(lines[i], &metadata))
		items = append(items, map[string]any{"index": map[string]any{"_id": metadata["index"]["_id"], "status": 201}})
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
	results, err := (&OpenSearchConnector{}).IndexDocuments(context.Background(), "index", []interfaces.DocumentWrite{{Document: map[string]any{"title": "missing ID"}}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 400, results[0].Status)
	assert.Equal(t, interfaces.DocumentInvalid, results[0].ErrorCode)
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

// datasetTransport 隔离传输依赖，回归测试不使用真实网络。
type datasetTransport func(*http.Request) (*http.Response, error)

func (f datasetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenSearchConnectorDeleteDocumentsByQuery(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		failed     bool
	}{
		{"complete", `{"deleted":3,"version_conflicts":0,"timed_out":false,"failures":[]}`, false},
		{"partial", `{"deleted":2,"version_conflicts":0,"timed_out":false,"failures":[{"status":400,"cause":{"reason":"secret source detail"}}]}`, true},
		{"http409", `{"deleted":2,"version_conflicts":1,"timed_out":false,"failures":[]}`, true},
		{"conflicts", `{"deleted":2,"version_conflicts":1,"timed_out":false,"failures":[]}`, true},
		{"timeout", `{"deleted":2,"version_conflicts":0,"timed_out":true,"failures":[]}`, true},
		{"malformed", `{`, true},
		{"empty", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := opensearch.NewClient(opensearch.Config{Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
				status := 200
				if tc.name == "http409" {
					status = 409
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			require.NoError(t, err)
			schema := []*interfaces.Property{{Name: "kind", Type: interfaces.DataType_String}}
			condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "kind", Operation: filter_condition.OperationEqual2, ValueFrom: interfaces.ValueFrom_Const, Value: "remove"}, map[string]*interfaces.Property{"kind": schema[0]})
			require.NoError(t, err)
			connector := &OpenSearchConnector{client: client}
			result, err := connector.DeleteDocumentsByQuery(context.Background(), "dataset-1", &interfaces.ResourceDataQueryParams{ActualFilterCond: condition}, schema)
			if tc.name == "http409" || tc.name == "partial" || tc.name == "timeout" || tc.name == "conflicts" {
				var incomplete *interfaces.DeleteByQueryIncompleteError
				require.ErrorAs(t, err, &incomplete)
				require.NotNil(t, result)
				assert.EqualValues(t, 2, result.Deleted)
				assert.Same(t, result, incomplete.Result)
				encoded, marshalErr := json.Marshal(result)
				require.NoError(t, marshalErr)
				assert.NotContains(t, string(encoded), "secret")
			}
			if tc.failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOpenSearchConnectorIndexDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		httpStatus   int
		transportErr error
	}{
		{"complete", `{"items":[{"index":{"_id":"a","status":201}},{"index":{"_id":"b","status":200}},{"index":{"_id":"c","status":201}},{"index":{"_id":"d","status":200}}]}`, 200, nil},
		{"partial", `{"errors":true,"items":[{"index":{"_id":"a","status":201}},{"index":{"_id":"b","status":409,"error":{"type":"mapper_parsing_exception","reason":"invalid mapping configuration"}}},{"index":{"_id":"c","status":429}},{"index":{"_id":"d","status":400}}]}`, 200, nil},
		{"malformed", `{`, 200, nil}, {"missing items", `{"items":[]}`, 200, nil},
		{"unauthorized", `{"error":"secret"}`, 401, nil},
		{"forbidden", `{"error":"secret"}`, 403, nil},
		{"rejected", `{"error":"secret"}`, 400, nil}, {"unavailable", `{"error":"secret"}`, 503, nil},
		{"transport failure", "", 200, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			sharedLogger := logger.GetLogger()
			original := *sharedLogger
			*sharedLogger = *zap.New(core).Sugar()
			t.Cleanup(func() { *sharedLogger = original })
			calls := 0
			client, err := opensearch.NewClient(opensearch.Config{DisableRetry: true, Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "/_bulk", r.URL.Path)
				assert.Equal(t, "true", r.URL.Query().Get("refresh"))
				decoder := json.NewDecoder(r.Body)
				for _, id := range []string{"a", "b", "c", "d"} {
					var meta, document map[string]any
					require.NoError(t, decoder.Decode(&meta))
					require.NoError(t, decoder.Decode(&document))
					assert.Equal(t, map[string]any{"index": map[string]any{"_index": "index", "_id": id}}, meta)
					assert.NotContains(t, document, "_id")
					assert.Equal(t, id, document["value"])
				}
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{StatusCode: tc.httpStatus, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			require.NoError(t, err)
			inputs := []interfaces.DocumentWrite{}
			for _, id := range []string{"a", "b", "c", "d"} {
				inputs = append(inputs, interfaces.DocumentWrite{ID: id, Document: map[string]any{"_id": "overridden", "value": id}})
			}
			results, err := (&OpenSearchConnector{client: client}).IndexDocuments(context.Background(), "index", inputs)
			require.NoError(t, err)
			require.Len(t, results, 4)
			assert.Equal(t, 1, calls)
			switch tc.name {
			case "complete":
				assert.Empty(t, logs.All())
				for _, result := range results {
					assert.GreaterOrEqual(t, result.Status, 200)
					assert.Less(t, result.Status, 300)
				}
			case "partial":
				require.Len(t, logs.All(), 3)
				assert.Contains(t, logs.All()[0].Message, "mapper_parsing_exception")
				assert.Contains(t, logs.All()[0].Message, "invalid mapping configuration")
				assert.Contains(t, logs.All()[0].Message, `index="index"`)
				assert.Contains(t, logs.All()[0].Message, `id="b"`)
				assert.Equal(t, 201, results[0].Status)
				assert.Empty(t, results[0].ErrorCode)
				assert.Equal(t, interfaces.DocumentConflict, results[1].ErrorCode)
				assert.True(t, results[2].Retryable)
				assert.Equal(t, interfaces.DocumentInvalid, results[3].ErrorCode)
			case "unauthorized", "forbidden":
				for i, result := range results {
					assert.Equal(t, inputs[i].ID, result.ID)
					assert.Equal(t, tc.httpStatus, result.Status)
					assert.Equal(t, interfaces.DocumentRejected, result.ErrorCode)
					assert.False(t, result.Retryable)
				}
			case "rejected":
				for _, result := range results {
					assert.Equal(t, 400, result.Status)
					assert.False(t, result.Retryable)
				}
			default:
				for _, result := range results {
					assert.Equal(t, interfaces.DocumentOutcomeUnknown, result.ErrorCode)
					assert.True(t, result.Retryable)
				}
			}
			if tc.name != "partial" && tc.name != "complete" {
				require.Len(t, logs.All(), 1)
				assert.Contains(t, logs.All()[0].Message, `index="index"`)
			}
			public, err := json.Marshal(results)
			require.NoError(t, err)
			assert.NotContains(t, string(public), "secret")
			assert.NotContains(t, string(public), "mapper_parsing_exception")
			assert.NotContains(t, string(public), "invalid mapping configuration")
			assert.NotContains(t, inputs[0].Document, "_id")
		})
	}
}

func TestOpenSearchConnectorIndexDocumentsSplitsByBytes(t *testing.T) {
	input := []interfaces.DocumentWrite{{ID: "a", Document: map[string]any{"value": "one"}}, {ID: "bad"}, {ID: "b", Document: map[string]any{"value": "two"}}}
	data, err := encodeBulkDocument("index", "a", map[string]any{"value": "one"}, 1024)
	require.NoError(t, err)
	input = []interfaces.DocumentWrite{
		input[0], input[1],
		{ID: "oversized", Document: map[string]any{"value": strings.Repeat("x", len(data)+1)}},
		{ID: "invalid-json", Document: map[string]any{"value": make(chan int)}},
		input[2],
	}
	calls := 0
	client, err := opensearch.NewClient(opensearch.Config{DisableRetry: true, Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(body), len(data))
		status, response := 503, `{"error":"unavailable"}`
		if calls == 2 {
			status, response = 200, `{"items":[{"index":{"_id":"b","status":201}}]}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	})})
	require.NoError(t, err)
	results, err := (&OpenSearchConnector{client: client, bulkRequestMaxBytes: len(data)}).IndexDocuments(context.Background(), "index", input)
	require.NoError(t, err)
	require.Len(t, results, 5)
	assert.Equal(t, 2, calls)
	assert.Equal(t, interfaces.DocumentOutcomeUnknown, results[0].ErrorCode)
	assert.Equal(t, interfaces.DocumentInvalid, results[1].ErrorCode)
	assert.Equal(t, interfaces.DocumentInvalid, results[2].ErrorCode)
	assert.Equal(t, interfaces.DocumentInvalid, results[3].ErrorCode)
	assert.Equal(t, 201, results[4].Status)
}

func TestOpenSearchConnectorIndexDocumentsRecoversRejectedBatch(t *testing.T) {
	for _, status := range []int{413, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			var counts []int
			client, err := opensearch.NewClient(opensearch.Config{DisableRetry: true, Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				decoder := json.NewDecoder(r.Body)
				ids := []string{}
				for {
					var meta map[string]map[string]string
					err := decoder.Decode(&meta)
					if err == io.EOF {
						break
					}
					require.NoError(t, err)
					var document map[string]any
					require.NoError(t, decoder.Decode(&document))
					ids = append(ids, meta["index"]["_id"])
				}
				counts = append(counts, len(ids))
				code := 200
				var body string
				if calls == 1 {
					code = status
					body = `{"error":{"type":"rejected_execution_exception"}}`
				} else {
					items := make([]map[string]any, len(ids))
					for i, id := range ids {
						items[i] = map[string]any{"index": map[string]any{"_id": id, "status": 201}}
					}
					encoded, err := json.Marshal(map[string]any{"items": items})
					require.NoError(t, err)
					body = string(encoded)
				}
				return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			require.NoError(t, err)
			results, err := (&OpenSearchConnector{client: client}).IndexDocuments(context.Background(), "index", []interfaces.DocumentWrite{{ID: "a", Document: map[string]any{"value": "one"}}, {ID: "b", Document: map[string]any{"value": "two"}}})
			require.NoError(t, err)
			assert.Equal(t, []int{2, 1, 1}, counts)
			require.Len(t, results, 2)
			assert.Equal(t, "a", results[0].ID)
			assert.Equal(t, "b", results[1].ID)
			for _, result := range results {
				assert.Equal(t, 201, result.Status)
				assert.Empty(t, result.ErrorCode)
			}
		})
	}
}

func TestOpenSearchConnectorCreateDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, id, response string
		ok                 bool
	}{
		{"generated ID", "", `{"items":[{"index":{"_id":"generated","status":201}}]}`, true},
		{"explicit ID", "chosen", `{"items":[{"index":{"_id":"chosen","status":201}}]}`, true},
		{"wrong returned ID", "chosen", `{"items":[{"index":{"_id":"other","status":201}}]}`, false},
		{"missing generated ID", "", `{"items":[{"index":{"_id":"","status":201}}]}`, false},
		{"item failure", "", `{"errors":true,"items":[{"index":{"_id":"generated","status":400,"error":{"reason":"secret source details"}}}]}`, false},
		{"invalid response", "", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := opensearch.NewClient(opensearch.Config{DisableRetry: true, Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
				decoder := json.NewDecoder(r.Body)
				var metadata map[string]map[string]string
				var document map[string]any
				require.NoError(t, decoder.Decode(&metadata))
				require.NoError(t, decoder.Decode(&document))
				if tc.id == "" {
					assert.NotContains(t, metadata["index"], "_id")
				} else {
					assert.Equal(t, tc.id, metadata["index"]["_id"])
				}
				assert.Equal(t, map[string]any{"content": "hello"}, document)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.response))}, nil
			})})
			require.NoError(t, err)
			document := map[string]any{"content": "hello"}
			if tc.id != "" {
				document["_id"] = tc.id
			}
			ids, err := (&OpenSearchConnector{client: client}).CreateDocuments(context.Background(), "index", []map[string]any{document})
			if tc.ok {
				require.NoError(t, err)
				want := "generated"
				if tc.id != "" {
					want = tc.id
				}
				assert.Equal(t, []string{want}, ids)
			} else {
				require.Error(t, err)
				assert.Nil(t, ids)
				assert.NotContains(t, err.Error(), "secret")
			}
		})
	}
	t.Run("creation stops after a failed byte chunk", func(t *testing.T) {
		calls := 0
		client, err := opensearch.NewClient(opensearch.Config{DisableRetry: true, Transport: datasetTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"items":[{"index":{"_id":"first","status":400}}]}`))}, nil
		})})
		require.NoError(t, err)
		first, err := encodeBulkDocument("index", "first", map[string]any{"value": "one"}, 1024)
		require.NoError(t, err)
		ids, err := (&OpenSearchConnector{client: client, bulkRequestMaxBytes: len(first)}).CreateDocuments(context.Background(), "index", []map[string]any{{"_id": "first", "value": "one"}, {"_id": "later", "value": "two"}})
		require.Error(t, err)
		assert.Nil(t, ids)
		assert.Equal(t, 1, calls)
	})
	t.Run("encoding failure prevents any write", func(t *testing.T) {
		ids, err := (&OpenSearchConnector{}).CreateDocuments(context.Background(), "index", []map[string]any{{"content": "hello"}, nil})
		require.Error(t, err)
		assert.Nil(t, ids)
	})
}
