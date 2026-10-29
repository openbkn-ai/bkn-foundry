// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_VegaAccess(t *testing.T) {
	Convey("Test vegaAccess\n", t, func() {
		type request struct {
			method, path, query, accountID, accountType string
			body                                        map[string]any
		}
		var got []request
		status, respBody := http.StatusOK, ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
				accountID: r.Header.Get("x-account-id"), accountType: r.Header.Get("x-account-type")}
			if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		va := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}
		ctx := context.Background()
		account := interfaces.VegaAccount{ID: "u1", Type: "user"}

		Convey("ListTables lists the catalog's tables in one page as the account\n", func() {
			respBody = `{"entries":[{"id":"r1","catalog_id":"c1","name":"orders","category":"table"},
				{"id":"r2","catalog_id":"c1","name":"files","category":"fileset"}],"total_count":2}`

			tables, err := va.ListTables(ctx, account, "c1")

			So(err, ShouldBeNil)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/resources")
			So(got[0].query, ShouldEqual, "catalog_id=c1&category=table&limit=-1")
			So(got[0].accountID, ShouldEqual, "u1")
			So(got[0].accountType, ShouldEqual, "user")
			So(len(tables), ShouldEqual, 1)
			So(tables[0].ID, ShouldEqual, "r1")
		})

		Convey("MissingCatalogs asks once when every catalog is there\n", func() {
			respBody = `{"entries":[{"id":"c 1"},{"id":"c2"}]}`

			missing, err := va.MissingCatalogs(ctx, account, []string{"c 1", "c2"})

			So(err, ShouldBeNil)
			So(missing, ShouldBeEmpty)
			So(len(got), ShouldEqual, 1)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/catalogs/c 1,c2")
			So(got[0].accountID, ShouldEqual, "u1")
		})

		Convey("MissingCatalogs asks each catalog when the batch is refused; only a deleted one is missing\n", func() {
			// vega-backend refuses the batch with 403 before reporting a missing id, and
			// answers 403 for a catalog the account lacks view_detail on although it may read
			// its tables, so only 404 means the catalog is gone.
			byPath := map[string]int{
				"/api/vega-backend/in/v1/catalogs/c1,c2,c3": http.StatusForbidden,
				"/api/vega-backend/in/v1/catalogs/c1":       http.StatusOK,
				"/api/vega-backend/in/v1/catalogs/c2":       http.StatusForbidden,
				"/api/vega-backend/in/v1/catalogs/c3":       http.StatusNotFound,
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(byPath[r.URL.Path])
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			each := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}

			missing, err := each.MissingCatalogs(ctx, account, []string{"c1", "c2", "c3"})

			So(err, ShouldBeNil)
			So(missing, ShouldResemble, []string{"c3"})

			byPath["/api/vega-backend/in/v1/catalogs/c1,c2,c3"] = http.StatusNotFound
			byPath["/api/vega-backend/in/v1/catalogs/c2"] = http.StatusInternalServerError
			_, err = each.MissingCatalogs(ctx, account, []string{"c1", "c2", "c3"})
			var vegaErr *VegaError
			So(errors.As(err, &vegaErr), ShouldBeTrue)
			So(vegaErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("GetTables reads the structure, with vega's types and the exact row count\n", func() {
			respBody = `{"entries":[{"id":"r1","catalog_id":"c1","name":"orders","row_count":800,
				"estimated_row_count":790,
				"schema_definition":[{"name":"order_id","type":"string","original_name":"order_id"},
					{"name":"customer_id","type":"integer","original_name":"customer_id"}],
				"source_metadata":{"name":"orders","schema":"erp","description":"Sales orders",
					"columns":[{"name":"order_id","type":"varchar(32)","description":"Order number","nullable":false},
						{"name":"customer_id","type":"int","description":"","nullable":true}],
					"primary_keys":["order_id"],
					"indices":[{"columns":["order_id"],"unique":true,"primary":true}],
					"foreign_keys":[{"columns":["customer_id"],"ref_table":"customers","ref_columns":["id"]}]}}]}`

			tables, err := va.GetTables(ctx, account, []string{"r1", "r 2"})

			So(err, ShouldBeNil)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/resources/r1,r 2")
			So(got[0].query, ShouldEqual, "ignore_missing=true")
			So(tables[0], ShouldResemble, &interfaces.VegaTable{
				ID: "r1", CatalogID: "c1", Name: "orders", Description: "Sales orders",
				SourceName: "orders", SourceSchema: "erp", Rows: 800, RowsCounted: true,
				Columns: []interfaces.VegaColumn{
					{Name: "order_id", Type: "string", Description: "Order number", Comment: "Order number"},
					{Name: "customer_id", Type: "integer", Nullable: true},
				},
				PrimaryKeys: []string{"order_id"},
				Indices:     []interfaces.VegaIndex{{Columns: []string{"order_id"}, Unique: true, Primary: true}},
				ForeignKeys: []interfaces.VegaForeignKey{{Columns: []string{"customer_id"}, RefTable: "customers", RefColumns: []string{"id"}}},
			})
		})

		Convey("GetTables takes the names and descriptions semantic understanding wrote back\n", func() {
			respBody = `{"entries":[{"id":"r1","name":"erp_material",
				"schema_definition":[
					{"name":"matnr","display_name":"Material code","type":"string","description":"Unique material code","original_name":"matnr"},
					{"name":"maktx","display_name":"maktx","type":"string","description":"","original_name":"maktx"}],
				"source_metadata":{"name":"erp_material","columns":[
					{"name":"matnr","type":"varchar(18)","description":"MATNR"},
					{"name":"maktx","type":"varchar(40)","description":"Short text"}]}}]}`

			tables, err := va.GetTables(ctx, account, []string{"r1"})

			So(err, ShouldBeNil)
			So(tables[0].Columns, ShouldResemble, []interfaces.VegaColumn{
				{Name: "matnr", DisplayName: "Material code", Type: "string", Description: "Unique material code", Comment: "MATNR"},
				{Name: "maktx", Type: "string", Description: "Short text", Comment: "Short text"},
			})
		})

		Convey("GetTables reads the table's local index: the fields a build needs, and its state\n", func() {
			respBody = `{"entries":[{"id":"r1","name":"orders","local_status":"stale","index_name":"idx_r1",
				"index_config":{"primary_key_fields":["id"],"incremental_fields":["updated_at"]}},
				{"id":"r2","name":"parts"}]}`

			tables, err := va.GetTables(ctx, account, []string{"r1", "r2"})

			So(err, ShouldBeNil)
			So(tables[0].Index, ShouldResemble, interfaces.VegaTableIndex{
				PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"updated_at"},
				LocalStatus: interfaces.VEGA_LOCAL_STATUS_STALE, IndexName: "idx_r1",
			})
			// A table never configured has no index config and no local status yet.
			So(tables[1].Index, ShouldResemble, interfaces.VegaTableIndex{})
		})

		Convey("GetTables reads each table when the batch is refused, leaving out those the account may not see\n", func() {
			// vega-backend refuses a batch holding one table the account lacks view_detail on.
			type reply struct {
				code int
				body string
			}
			byPath := map[string]reply{
				"/api/vega-backend/in/v1/resources/r1,r2,r3": {http.StatusForbidden, `{}`},
				"/api/vega-backend/in/v1/resources/r1":       {http.StatusOK, `{"entries":[{"id":"r1","name":"orders"}]}`},
				"/api/vega-backend/in/v1/resources/r2":       {http.StatusForbidden, `{}`},
				"/api/vega-backend/in/v1/resources/r3":       {http.StatusOK, `{"entries":[]}`},
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(byPath[r.URL.Path].code)
				_, _ = w.Write([]byte(byPath[r.URL.Path].body))
			}))
			defer server.Close()
			each := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}

			tables, err := each.GetTables(ctx, account, []string{"r1", "r2", "r3"})

			So(err, ShouldBeNil)
			So(len(tables), ShouldEqual, 1)
			So(tables[0].ID, ShouldEqual, "r1")

			byPath["/api/vega-backend/in/v1/resources/r3"] = reply{http.StatusInternalServerError, `{}`}
			_, err = each.GetTables(ctx, account, []string{"r1", "r2", "r3"})
			So(err, ShouldNotBeNil)
		})

		Convey("ListActiveBuildTasks lists a catalog's active tasks only for an account that may manage them\n", func() {
			// vega-backend lists nothing, with 200, for a catalog the account lacks task_manage on.
			type reply struct {
				code int
				body string
			}
			tasksBody := `{"entries":[{"id":"t1","resource_id":"r1","status":"running","mode":"batch"}],"total_count":1}`
			byPath := map[string]reply{
				"/api/vega-backend/in/v1/catalogs/c1": {http.StatusOK, `{"entries":[{"id":"c1","operations":["view_detail","task_manage"]}]}`},
				"/api/vega-backend/in/v1/catalogs/c2": {http.StatusOK, `{"entries":[{"id":"c2","operations":["view_detail"]}]}`},
				"/api/vega-backend/in/v1/catalogs/c3": {http.StatusForbidden, `{}`},
				"/api/vega-backend/in/v1/catalogs/c4": {http.StatusInternalServerError, `{}`},
				"/api/vega-backend/in/v1/build-tasks": {http.StatusOK, tasksBody},
			}
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/vega-backend/in/v1/build-tasks" {
					queries = append(queries, r.URL.RawQuery)
				}
				w.WriteHeader(byPath[r.URL.Path].code)
				_, _ = w.Write([]byte(byPath[r.URL.Path].body))
			}))
			defer server.Close()
			each := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}

			tasks, visible, err := each.ListActiveBuildTasks(ctx, account, "c1")
			So(err, ShouldBeNil)
			So(visible, ShouldBeTrue)
			So(tasks, ShouldResemble, []*interfaces.VegaBuildTask{{ID: "t1", ResourceID: "r1", Status: "running"}})
			So(queries, ShouldResemble, []string{"catalog_id=c1&limit=-1&status=pending&status=running&status=stopping"})

			for _, catalogID := range []string{"c2", "c3"} {
				tasks, visible, err = each.ListActiveBuildTasks(ctx, account, catalogID)
				So(err, ShouldBeNil)
				So(visible, ShouldBeFalse)
				So(tasks, ShouldBeEmpty)
			}
			// Neither asked for tasks it may not see.
			So(len(queries), ShouldEqual, 1)

			_, _, err = each.ListActiveBuildTasks(ctx, account, "c4")
			So(err, ShouldNotBeNil)
		})

		Convey("GetTables takes the English short names semantic understanding gave\n", func() {
			respBody = `{"entries":[{"id":"r1","name":"供应商档案","api_name":"supplier",
				"schema_definition":[
					{"name":"编号","display_name":"供应商编号","type":"string","original_name":"编号","api_name":"supplier_code"},
					{"name":"电话","display_name":"电话","type":"string","original_name":"电话"}],
				"source_metadata":{"name":"供应商","columns":[
					{"name":"编号","type":"varchar(18)"},
					{"name":"电话","type":"varchar(20)"}]}}]}`

			tables, err := va.GetTables(ctx, account, []string{"r1"})

			So(err, ShouldBeNil)
			So(tables[0].APIName, ShouldEqual, "supplier")
			So([]string{tables[0].Columns[0].APIName, tables[0].Columns[1].APIName}, ShouldResemble, []string{"supplier_code", ""})
		})

		Convey("GetTables falls back to the estimated row count, and asks nothing for no ids\n", func() {
			respBody = `{"entries":[{"id":"r1","estimated_row_count":42}]}`

			tables, err := va.GetTables(ctx, account, []string{"r1"})
			So(err, ShouldBeNil)
			So(tables[0].Rows, ShouldEqual, 42)
			So(tables[0].RowsCounted, ShouldBeFalse)

			none, err := va.GetTables(ctx, account, nil)
			So(err, ShouldBeNil)
			So(none, ShouldBeEmpty)
			So(len(got), ShouldEqual, 1)
		})

		Convey("Query posts the SQL with its timeout and returns the rows\n", func() {
			respBody = `{"columns":[{"name":"total"}],"entries":[{"total":800}]}`

			rows, err := va.Query(ctx, account, "SELECT COUNT(*) AS total FROM {{r1}}", 60)

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/resources/query")
			So(got[0].body["query"], ShouldEqual, "SELECT COUNT(*) AS total FROM {{r1}}")
			So(got[0].body["query_timeout_sec"], ShouldEqual, 60)
			So(rows[0]["total"], ShouldEqual, 800)
		})

		Convey("A vega-backend error keeps its status and code\n", func() {
			status, respBody = http.StatusNotImplemented, `{"error_code":"VegaBackend.Query.MultiCatalogNotSupported"}`

			_, err := va.Query(ctx, account, "SELECT 1", 60)

			var vegaErr *VegaError
			So(errors.As(err, &vegaErr), ShouldBeTrue)
			So(vegaErr.HTTPCode, ShouldEqual, http.StatusNotImplemented)
			So(vegaErr.ErrorCode, ShouldEqual, "VegaBackend.Query.MultiCatalogNotSupported")
		})
	})
}
