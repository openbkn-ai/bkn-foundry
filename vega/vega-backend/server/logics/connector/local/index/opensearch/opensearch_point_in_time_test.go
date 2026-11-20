// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package opensearch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	opensearchclient "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestOpenSearchIndexReadSession(t *testing.T) {
	for _, phase := range []string{
		"pages", "raw pages", "raw source disabled", "expired", "partial search", "partial snapshot",
		"missing total", "inexact raw total", "missing source", "invalid source", "missing final sort",
		"raw missing hits", "raw null hits", "raw empty hits", "short before exact total",
	} {
		t.Run(phase, func(t *testing.T) {
			pageSize := 1
			switch phase {
			case "missing source", "invalid source", "missing final sort", "short before exact total":
				pageSize = 2
			}
			var searches, deletes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/events/_search/point_in_time":
					assert.Equal(t, "60000ms", r.URL.Query().Get("keep_alive"))
					assert.Equal(t, "false", r.URL.Query().Get("allow_partial_pit_creation"))
					if phase == "partial snapshot" {
						_, _ = io.WriteString(w, `{"pit_id":"snapshot","_shards":{"failed":1}}`)
					} else {
						_, _ = io.WriteString(w, `{"pit_id":"snapshot","_shards":{"failed":0}}`)
					}
				case r.URL.Path == "/_search":
					searchNumber := searches.Add(1)
					assert.Equal(t, "false", r.URL.Query().Get("allow_partial_search_results"))
					var body map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					if !assert.NoError(t, decoder.Decode(&body)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.Equal(t, map[string]any{"id": "snapshot", "keep_alive": "60000ms"}, body["pit"])
					assert.Equal(t, []any{map[string]any{"id": map[string]any{"order": "asc"}}}, body["sort"])
					assert.Equal(t, json.Number(strconv.Itoa(pageSize)), body["size"])
					switch {
					case phase == "expired":
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"error":"expired"}`)
					case phase == "partial search":
						_, _ = io.WriteString(w, `{"timed_out":true,"hits":{"hits":[]}}`)
					case phase == "missing total":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"hits":[]}}`)
					case phase == "inexact raw total":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":1,"relation":"gte"},"hits":[]}}`)
					case phase == "missing source":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":7,"relation":"eq"},"hits":[{"_id":"5","_source":{},"sort":[5]},{"_id":"6","sort":[6]}]}}`)
					case phase == "invalid source":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":7,"relation":"eq"},"hits":[{"_id":"5","_source":{},"sort":[5]},{"_id":"6","_source":[],"sort":[6]}]}}`)
					case phase == "missing final sort":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":7,"relation":"eq"},"hits":[{"_id":"5","_source":{},"sort":[5]},{"_id":"6","_source":{}}]}}`)
					case phase == "short before exact total":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":7,"relation":"eq"},"hits":[{"_id":"5","_source":{},"sort":[5]}]}}`)
					case phase == "raw missing hits":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":7,"relation":"eq"}}}`)
					case phase == "raw null hits":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":7,"relation":"eq"},"hits":null}}`)
					case phase == "raw empty hits":
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)
					case phase == "raw source disabled":
						assert.Equal(t, false, body["_source"])
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":5,"relation":"eq"},"hits":[{"_id":"5","sort":[5]}]}}`)
					case searchNumber == 1:
						assert.Equal(t, true, body["track_total_hits"])
						assert.Equal(t, json.Number("4"), body["from"])
						assert.NotContains(t, body, "search_after")
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":6,"relation":"eq"},"hits":[{"_index":"events","_id":"5","_source":{},"sort":[9007199254740993]}]}}`)
					default:
						assert.Equal(t, false, body["track_total_hits"])
						assert.NotContains(t, body, "from")
						assert.Equal(t, []any{json.Number("9007199254740993")}, body["search_after"])
						_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"hits":[{"_index":"events","_id":"6","_source":{"id":6},"sort":[9007199254740994]}]}}`)
					}
				case r.Method == http.MethodDelete && r.URL.Path == "/_search/point_in_time":
					deletes.Add(1)
					var body struct {
						IDs []string `json:"pit_id"`
					}
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					assert.Equal(t, []string{"snapshot"}, body.IDs)
					_, _ = io.WriteString(w, `{"pits":[{"pit_id":"snapshot","successful":true}]}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			client, err := opensearchclient.NewClient(opensearchclient.Config{Addresses: []string{server.URL}})
			require.NoError(t, err)
			connector := &OpenSearchConnector{client: client}
			resource := &interfaces.Resource{SourceIdentifier: "events", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
			var session interfaces.QueryPageSession
			switch phase {
			case "raw pages", "raw source disabled", "inexact raw total", "raw missing hits", "raw null hits", "raw empty hits":
				dsl := map[string]any{
					"sort": []any{map[string]any{"id": map[string]any{"order": "asc"}}},
					"from": 4, "track_total_hits": true,
				}
				if phase == "raw source disabled" {
					dsl["_source"] = false
				}
				session, err = connector.OpenRawIndexRead(t.Context(), resource, dsl, time.Minute)
			default:
				session, err = connector.OpenIndexRead(t.Context(), "events", resource, &interfaces.ResourceDataQueryParams{
					Sort: []*interfaces.SortField{{Field: "id", Direction: "asc"}}, NeedTotal: true,
					Paging: interfaces.PagingRequest{Offset: 4},
				}, time.Minute)
			}
			if phase == "partial snapshot" {
				require.ErrorContains(t, err, "incomplete index snapshot")
				assert.EqualValues(t, 1, deletes.Load())
				assert.Zero(t, searches.Load())
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, session.Close()) })
			first, err := session.ReadPage(t.Context(), pageSize)
			switch phase {
			case "expired":
				require.ErrorIs(t, err, interfaces.ErrQueryPageSessionExpired)
			case "partial search":
				require.ErrorContains(t, err, "index search timed out")
			case "missing total", "inexact raw total":
				require.ErrorContains(t, err, "index search has no exact total")
			case "missing source", "invalid source":
				require.ErrorContains(t, err, "index search hit has no _source object")
				assert.Nil(t, first)
			case "missing final sort":
				require.ErrorContains(t, err, "index cursor page is missing its sort values")
				assert.Nil(t, first)
			case "short before exact total":
				require.ErrorContains(t, err, "index cursor page size does not match its exact total")
				assert.Nil(t, first)
			case "raw missing hits", "raw null hits":
				require.ErrorContains(t, err, "index search response has no document list")
				assert.Nil(t, first)
			case "raw empty hits":
				require.NoError(t, err)
				assert.False(t, first.HasMore)
				assert.Empty(t, first.Entries)
				require.NotNil(t, first.TotalCount)
				assert.Zero(t, *first.TotalCount)
			case "raw source disabled":
				require.NoError(t, err)
				assert.False(t, first.HasMore)
				require.Len(t, first.Entries, 1)
			default:
				require.NoError(t, err)
				assert.True(t, first.HasMore)
				require.NotNil(t, first.TotalCount)
				assert.EqualValues(t, 6, *first.TotalCount)
				if phase == "pages" {
					assert.Equal(t, []map[string]any{{"_id": "5"}}, first.Entries)
				}
				second, err := session.ReadPage(t.Context(), 1)
				require.NoError(t, err)
				assert.False(t, second.HasMore)
				if phase == "raw pages" {
					assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}}, second.Columns)
					assert.Equal(t, json.Number("6"), second.Entries[0]["id"])
				} else {
					assert.Equal(t, "6", second.Entries[0]["_id"])
				}
				assert.Equal(t, first.TotalCount, second.TotalCount)
			}
			require.NoError(t, session.Close())
			require.NoError(t, session.Close())
			assert.EqualValues(t, 1, deletes.Load())
		})
	}
}

func TestOpenSearchConnectorOpenRawIndexReadUsesResourceSchema(t *testing.T) {
	var searches atomic.Int32
	var closed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/events-alias/_search/point_in_time":
			_, _ = io.WriteString(w, `{"pit_id":"snapshot","_shards":{"failed":0}}`)
		case r.URL.Path == "/_search":
			if searches.Add(1) == 1 {
				_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"total":{"value":2,"relation":"eq"},"hits":[{"_index":"events-000001","_source":{"payload":{"n":1},"archived":{"n":10},"public_payload":{"n":100}},"sort":[1]}]}}`)
			} else {
				_, _ = io.WriteString(w, `{`+completeSearchMetadata+`,"hits":{"hits":[{"_index":"events-000002","_source":{"payload":{"n":2},"archived":{"n":20},"public_payload":{"n":200}},"sort":[2]}]}}`)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/_search/point_in_time":
			closed.Store(true)
			_, _ = io.WriteString(w, `{"pits":[{"pit_id":"snapshot","successful":true}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := opensearchclient.NewClient(opensearchclient.Config{Addresses: []string{server.URL}})
	require.NoError(t, err)
	connector := &OpenSearchConnector{client: client}
	property := &interfaces.Property{Name: "public_payload", OriginalName: "payload", Type: interfaces.DataType_Json}
	resource := &interfaces.Resource{
		SourceIdentifier: "events-alias",
		SchemaDefinition: []*interfaces.Property{property},
		SourceMetadata:   map[string]any{"object_fields": []any{"archived"}},
	}
	session, err := connector.OpenRawIndexRead(t.Context(), resource,
		map[string]any{"sort": []any{"id"}, "track_total_hits": true}, time.Minute)
	require.NoError(t, err)
	// 每页都使用打开会话时得到的类型。
	property.Type = interfaces.DataType_String
	delete(resource.SourceMetadata, "object_fields")
	first, err := session.ReadPage(t.Context(), 1)
	require.NoError(t, err)
	assert.True(t, first.HasMore)
	assert.ElementsMatch(t, []interfaces.ColumnInfo{
		{Name: "payload", Type: interfaces.DataType_Json},
		{Name: "archived", Type: interfaces.DataType_Json},
		{Name: "public_payload", Type: interfaces.DataType_String},
	}, first.Columns)
	assert.Equal(t, []map[string]any{{"payload": `{"n":1}`, "archived": `{"n":10}`, "public_payload": map[string]any{"n": json.Number("100")}}}, first.Entries)

	second, err := session.ReadPage(t.Context(), 1)
	require.NoError(t, err)
	assert.False(t, second.HasMore)
	assert.ElementsMatch(t, first.Columns, second.Columns)
	assert.Equal(t, []map[string]any{{"payload": `{"n":2}`, "archived": `{"n":20}`, "public_payload": map[string]any{"n": json.Number("200")}}}, second.Entries)
	require.NoError(t, session.Close())
	assert.True(t, closed.Load())
}
