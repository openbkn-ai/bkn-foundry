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

		Convey("CatalogVisible reads the catalog as the account; gone or hidden is not visible\n", func() {
			respBody = `{"entries":[{"id":"c 1"}]}`
			visible, err := va.CatalogVisible(ctx, account, "c 1")
			So(err, ShouldBeNil)
			So(visible, ShouldBeTrue)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/catalogs/c 1")
			So(got[0].accountID, ShouldEqual, "u1")

			// vega-backend answers 404 for a catalog that is gone or that the account cannot
			// see, and 403 for a built-in catalog.
			for _, code := range []int{http.StatusNotFound, http.StatusForbidden} {
				status, respBody = code, `{"error_code":"VegaBackend.Catalog.NotFound"}`
				visible, err = va.CatalogVisible(ctx, account, "c1")
				So(err, ShouldBeNil)
				So(visible, ShouldBeFalse)
			}

			status = http.StatusInternalServerError
			_, err = va.CatalogVisible(ctx, account, "c1")
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
