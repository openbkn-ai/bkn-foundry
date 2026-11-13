// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// page is a list page of entries with ids prefix0..prefixN-1 from offset, of total in all.
func page(prefix string, offset int, n int, total int) string {
	entries := []map[string]any{}
	for i := offset; i < offset+n; i++ {
		entries = append(entries, map[string]any{"id": fmt.Sprintf("%s%d", prefix, i)})
	}
	data, _ := json.Marshal(map[string]any{"entries": entries, "total_count": total})
	return string(data)
}

func Test_BknBackendAccess_Kinetic(t *testing.T) {
	Convey("Test bknBackendAccess reads of a network's implementations\n", t, func() {
		type request struct {
			path, auth string
			query      url.Values
		}
		var got []request
		reply := func(r *http.Request) (int, string) { return http.StatusOK, `{"entries":[],"total_count":0}` }
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = append(got, request{path: r.URL.EscapedPath(), auth: r.Header.Get("Authorization"), query: r.URL.Query()})
			status, body := reply(r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()
		bba := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
		ctx := context.Background()
		const networkPath = "/api/bkn-backend/v1/knowledge-networks/kn%201"
		offsetOf := func(r *http.Request) int {
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			return offset
		}
		metricJSON := `{"definition":{"id":"m1","version":3,"code":"late_rate","name":"Late rate","description":"d",
			"owner_object_type_id":"order","metric_type":"atomic","lifecycle":{"status":"published"}},
			"owner_object_type_name":"Order"}`
		lateRate := &interfaces.KNObjectMetric{ID: "m1", Version: 3, Code: "late_rate", Name: "Late rate", Description: "d",
			OwnerObjectTypeID: "order", OwnerObjectTypeName: "Order", MetricType: "atomic", Status: "published"}

		Convey("ListObjectMetrics reads the draft a page at a time, of the owner object type, and maps each definition\n", func() {
			reply = func(r *http.Request) (int, string) {
				if offsetOf(r) == 0 {
					entries := []string{metricJSON}
					for i := 1; i < listPageSize; i++ {
						entries = append(entries, fmt.Sprintf(`{"definition":{"id":"m%d"}}`, i+1))
					}
					return http.StatusOK, fmt.Sprintf(`{"entries":[%s],"total_count":%d}`, strings.Join(entries, ","), listPageSize+1)
				}
				return http.StatusOK, fmt.Sprintf(`{"entries":[{"definition":{"id":"last"}}],"total_count":%d}`, listPageSize+1)
			}

			metrics, err := bba.ListObjectMetrics(ctx, token, "kn 1", "order")

			So(err, ShouldBeNil)
			So(metrics, ShouldHaveLength, listPageSize+1)
			So(metrics[0], ShouldResemble, lateRate)
			So(metrics[listPageSize].ID, ShouldEqual, "last")
			So(got, ShouldHaveLength, 2)
			for i, g := range got {
				So(g.path, ShouldEqual, networkPath+"/object-metrics")
				So(g.auth, ShouldEqual, token)
				So(g.query, ShouldResemble, url.Values{"kn_version": {"draft"}, "owner_object_type_id": {"order"},
					"offset": {strconv.Itoa(i * listPageSize)}, "limit": {strconv.Itoa(listPageSize)}})
			}
		})

		Convey("ListObjectMetrics of every owner sends none, and of none is empty\n", func() {
			metrics, err := bba.ListObjectMetrics(ctx, token, "kn 1", "")

			So(err, ShouldBeNil)
			So(metrics, ShouldNotBeNil)
			So(metrics, ShouldBeEmpty)
			So(got[0].query.Has("owner_object_type_id"), ShouldBeFalse)
		})

		Convey("GetObjectMetric reads one metric of the draft\n", func() {
			reply = func(*http.Request) (int, string) { return http.StatusOK, metricJSON }

			metric, err := bba.GetObjectMetric(ctx, token, "kn 1", "m/1")

			So(err, ShouldBeNil)
			So(metric, ShouldResemble, lateRate)
			So(got[0].path, ShouldEqual, networkPath+"/object-metrics/m%2F1")
			So(got[0].auth, ShouldEqual, token)
			So(got[0].query, ShouldResemble, url.Values{"kn_version": {"draft"}})
		})

		Convey("ListCapabilities reads the bound capabilities with their detail, of a type and metadata type\n", func() {
			reply = func(r *http.Request) (int, string) {
				return http.StatusOK, `{"entries":[{"capability_type":"function","capability_id":"f1","box_id":"b1","name":"Rates",
					"description":"d","metadata_type":"openapi","status":"enabled"}],"total_count":1}`
			}

			capabilities, err := bba.ListCapabilities(ctx, token, "kn 1", "function", "openapi")

			So(err, ShouldBeNil)
			So(capabilities, ShouldResemble, []*interfaces.KNCapability{{CapabilityType: "function", CapabilityID: "f1",
				BoxID: "b1", Name: "Rates", Description: "d", MetadataType: "openapi", Status: "enabled"}})
			So(got, ShouldHaveLength, 1)
			So(got[0].path, ShouldEqual, networkPath+"/capabilities")
			So(got[0].auth, ShouldEqual, token)
			So(got[0].query, ShouldResemble, url.Values{"kn_version": {"draft"}, "with_detail": {"true"}, "type": {"function"},
				"metadata_type": {"openapi"}, "offset": {"0"}, "limit": {strconv.Itoa(listPageSize)}})

			got = nil
			_, err = bba.ListCapabilities(ctx, token, "kn 1", "", "")
			So(err, ShouldBeNil)
			So(got[0].query.Has("type"), ShouldBeFalse)
			So(got[0].query.Has("metadata_type"), ShouldBeFalse)
		})

		Convey("ListActionTypes reads the action types of an object type, with the tool each runs\n", func() {
			reply = func(r *http.Request) (int, string) {
				return http.StatusOK, `{"entries":[{"id":"a1","name":"Hold","object_type_id":"order","action_type":"modify",
					"action_source":{"type":"mcp","mcp_id":"mcp1","tool_name":"hold"}}],"total_count":1}`
			}

			actionTypes, err := bba.ListActionTypes(ctx, token, "kn 1", "order")

			So(err, ShouldBeNil)
			So(actionTypes, ShouldResemble, []*interfaces.KNActionType{{ID: "a1", Name: "Hold", ObjectTypeID: "order",
				ActionType: "modify", Source: interfaces.KNActionTypeSource{Type: "mcp", McpID: "mcp1", ToolName: "hold"}}})
			So(got[0].path, ShouldEqual, networkPath+"/action-types")
			So(got[0].auth, ShouldEqual, token)
			So(got[0].query, ShouldResemble, url.Values{"kn_version": {"draft"}, "object_type_id": {"order"},
				"offset": {"0"}, "limit": {strconv.Itoa(listPageSize)}})

			got = nil
			_, err = bba.ListActionTypes(ctx, token, "kn 1", "")
			So(err, ShouldBeNil)
			So(got[0].query.Has("object_type_id"), ShouldBeFalse)
		})

		Convey("ListObjectTypeIDs reads every page up to the total\n", func() {
			reply = func(r *http.Request) (int, string) {
				if offsetOf(r) == 0 {
					return http.StatusOK, page("ot", 0, listPageSize, listPageSize+2)
				}
				return http.StatusOK, page("ot", offsetOf(r), 2, listPageSize+2)
			}

			ids, err := bba.ListObjectTypeIDs(ctx, token, "kn 1")

			So(err, ShouldBeNil)
			So(ids, ShouldHaveLength, listPageSize+2)
			So(ids[0], ShouldEqual, "ot0")
			So(ids[listPageSize+1], ShouldEqual, fmt.Sprintf("ot%d", listPageSize+1))
			So(got, ShouldHaveLength, 2)
			So(got[0].path, ShouldEqual, networkPath+"/object-types")
			So(got[0].auth, ShouldEqual, token)
			So(got[1].query, ShouldResemble, url.Values{"kn_version": {"draft"}, "offset": {strconv.Itoa(listPageSize)},
				"limit": {strconv.Itoa(listPageSize)}})
		})

		Convey("ListObjectTypeSources reads each object type with the data it reads\n", func() {
			reply = func(r *http.Request) (int, string) {
				return http.StatusOK, `{"entries":[{"id":"order","name":"Order","data_source":{"type":"resource","id":"r1","name":"orders"}},
					{"id":"note"}],"total_count":2}`
			}

			objectTypes, err := bba.ListObjectTypeSources(ctx, token, "kn 1")

			So(err, ShouldBeNil)
			So(objectTypes, ShouldResemble, []*interfaces.KNObjectTypeSource{
				{ID: "order", DataSource: &interfaces.KNDataSource{Type: interfaces.KN_DATA_SOURCE_RESOURCE, ID: "r1"}},
				{ID: "note"},
			})
			So(got[0].path, ShouldEqual, networkPath+"/object-types")
			So(got[0].auth, ShouldEqual, token)
		})

		Convey("A list stops at its cap however many the network has\n", func() {
			reply = func(r *http.Request) (int, string) {
				return http.StatusOK, page("ot", offsetOf(r), listPageSize, 100000)
			}

			ids, err := bba.ListObjectTypeIDs(ctx, token, "kn 1")

			So(err, ShouldBeNil)
			So(ids, ShouldHaveLength, maxListed)
			So(got, ShouldHaveLength, maxListed/listPageSize)
		})

		Convey("A refusal is a bkn-backend error with its code; a plain-text 404 has none\n", func() {
			reply = func(*http.Request) (int, string) {
				return http.StatusForbidden, `{"error_code":"BknBackend.Forbidden","description":"no"}`
			}
			calls := []func() error{
				func() error { _, err := bba.ListObjectMetrics(ctx, token, "kn 1", ""); return err },
				func() error { _, err := bba.GetObjectMetric(ctx, token, "kn 1", "m1"); return err },
				func() error { _, err := bba.ListCapabilities(ctx, token, "kn 1", "", ""); return err },
				func() error { _, err := bba.ListActionTypes(ctx, token, "kn 1", ""); return err },
				func() error { _, err := bba.ListObjectTypeIDs(ctx, token, "kn 1"); return err },
			}
			for _, call := range calls {
				var backendErr *interfaces.BknBackendError
				So(errors.As(call(), &backendErr), ShouldBeTrue)
				So(backendErr.HTTPCode, ShouldEqual, http.StatusForbidden)
				So(backendErr.ErrorCode, ShouldEqual, "BknBackend.Forbidden")
			}

			reply = func(*http.Request) (int, string) { return http.StatusNotFound, "404 page not found" }
			_, err := bba.GetObjectMetric(ctx, token, "kn 1", "m1")
			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusNotFound)
			So(backendErr.ErrorCode, ShouldBeEmpty)

			// A failure on a later page fails the list.
			reply = func(r *http.Request) (int, string) {
				if offsetOf(r) == 0 {
					return http.StatusOK, page("ot", 0, listPageSize, listPageSize+1)
				}
				return http.StatusInternalServerError, `{"error_code":"BknBackend.InternalError"}`
			}
			_, err = bba.ListObjectTypeIDs(ctx, token, "kn 1")
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("A reply that is not the list is an error\n", func() {
			reply = func(*http.Request) (int, string) { return http.StatusOK, `{"entries":"nope"}` }

			_, err := bba.ListActionTypes(ctx, token, "kn 1", "")

			So(err, ShouldNotBeNil)
			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeFalse)
		})
	})
}

func Test_ListPagesWithoutTotal(t *testing.T) {
	Convey("A list that gives no total goes on to the next page while pages are full\n", t, func() {
		offsets := []int{}
		err := listPages(func(offset int) (int, int, error) {
			offsets = append(offsets, offset)
			if offset == 0 {
				return listPageSize, 0, nil
			}
			return 3, 0, nil
		})
		So(err, ShouldBeNil)
		So(offsets, ShouldResemble, []int{0, listPageSize})
	})
}
