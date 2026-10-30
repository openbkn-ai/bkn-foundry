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
			var vegaErr *interfaces.VegaError
			So(errors.As(err, &vegaErr), ShouldBeTrue)
			So(vegaErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("MissingTables asks once and takes the ids left out for missing\n", func() {
			respBody = `{"entries":[{"id":"r1","name":"orders"},{"id":"r3","name":"parts"}]}`

			missing, err := va.MissingTables(ctx, account, []string{"r1", "r 2", "r3"})

			So(err, ShouldBeNil)
			So(missing, ShouldResemble, []string{"r 2"})
			So(len(got), ShouldEqual, 1)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/resources/r1,r 2,r3")
			So(got[0].query, ShouldEqual, "ignore_missing=true")
			So(got[0].accountID, ShouldEqual, "u1")

			got, respBody = nil, `{"entries":[{"id":"r1"}]}`
			missing, err = va.MissingTables(ctx, account, []string{"r1"})
			So(err, ShouldBeNil)
			So(missing, ShouldBeEmpty)

			got = nil
			missing, err = va.MissingTables(ctx, account, nil)
			So(err, ShouldBeNil)
			So(missing, ShouldBeEmpty)
			So(got, ShouldBeEmpty)
		})

		Convey("MissingTables asks each table when the batch is refused; a refused table is not missing\n", func() {
			// vega-backend looks ids up before checking view_detail: a deleted table is left out with
			// 200, and only a table that exists can refuse, then for the whole batch.
			byPath := map[string]struct {
				code int
				body string
			}{
				"/api/vega-backend/in/v1/resources/r1,r2,r3": {http.StatusForbidden, `{"error_code":"Public.Forbidden"}`},
				"/api/vega-backend/in/v1/resources/r1":       {http.StatusOK, `{"entries":[{"id":"r1"}]}`},
				"/api/vega-backend/in/v1/resources/r2":       {http.StatusForbidden, `{"error_code":"Public.Forbidden"}`},
				"/api/vega-backend/in/v1/resources/r3":       {http.StatusOK, `{"entries":[]}`},
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reply := byPath[r.URL.Path]
				w.WriteHeader(reply.code)
				_, _ = w.Write([]byte(reply.body))
			}))
			defer server.Close()
			each := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}

			missing, err := each.MissingTables(ctx, account, []string{"r1", "r2", "r3"})

			So(err, ShouldBeNil)
			So(missing, ShouldResemble, []string{"r3"})

			// One table alone refused, as a built-in table is for a non-admin: present.
			missing, err = each.MissingTables(ctx, account, []string{"r2"})
			So(err, ShouldBeNil)
			So(missing, ShouldBeEmpty)

			byPath["/api/vega-backend/in/v1/resources/r3"] = struct {
				code int
				body string
			}{http.StatusInternalServerError, `{"error_code":"VegaBackend.Resource.InternalError"}`}
			_, err = each.MissingTables(ctx, account, []string{"r1", "r2", "r3"})
			var vegaErr *interfaces.VegaError
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

		Convey("UpdateIndexConfig fills only the keys the table lacks and sends the resource back as read\n", func() {
			type reply struct {
				code int
				body string
			}
			resourcePath := "/api/vega-backend/in/v1/resources/r1"
			read := `{"entries":[{"id":"r1","catalog_id":"c1","category":"table","name":"orders","description":"Orders",
				"tags":["erp"],"enabled":true,"update_time":7,"api_name":"orders",
				"index_config":{"primary_key_fields":["id"],"default_fulltext_analyzer":"ik"},
				"schema_definition":[{"name":"id","type":"integer","display_name":"No.","features":[]},
					{"name":"updated_at","type":"datetime"}]}]}`
			replies := map[string]reply{http.MethodGet: {http.StatusOK, read}, http.MethodPut: {http.StatusNoContent, ``}}
			var sent []request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req := request{method: r.Method, path: r.URL.Path, accountID: r.Header.Get("x-account-id")}
				if data, _ := io.ReadAll(r.Body); len(data) > 0 {
					_ = json.Unmarshal(data, &req.body)
				}
				sent = append(sent, req)
				w.WriteHeader(replies[r.Method].code)
				_, _ = w.Write([]byte(replies[r.Method].body))
			}))
			defer server.Close()
			each := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}
			keys := interfaces.VegaIndexKeys{PrimaryKeyFields: []string{"code"}, IncrementalFields: []string{"updated_at"}}

			Convey("A key set already stays; what vega-backend overwrites goes back as read\n", func() {
				changed, err := each.UpdateIndexConfig(ctx, account, "r1", keys)

				So(err, ShouldBeNil)
				So(changed, ShouldBeTrue)
				So(len(sent), ShouldEqual, 2)
				So(sent[0].method, ShouldEqual, http.MethodGet)
				So(sent[0].path, ShouldEqual, resourcePath)
				So(sent[1].method, ShouldEqual, http.MethodPut)
				So(sent[1].path, ShouldEqual, resourcePath)
				So(sent[1].accountID, ShouldEqual, "u1")
				So(sent[1].body, ShouldResemble, map[string]any{
					"name": "orders", "description": "Orders", "tags": []any{"erp"}, "category": "table",
					"catalog_id": "c1", "enabled": true, "expected_update_time": float64(7),
					"index_config": map[string]any{"primary_key_fields": []any{"id"},
						"incremental_fields": []any{"updated_at"}, "default_fulltext_analyzer": "ik"},
					// Sent back so that vega-backend adds the default features a build needs.
					"schema_definition": []any{
						map[string]any{"name": "id", "type": "integer", "display_name": "No.", "features": []any{}},
						map[string]any{"name": "updated_at", "type": "datetime"}},
				})
			})

			Convey("Nothing to fill writes nothing\n", func() {
				changed, err := each.UpdateIndexConfig(ctx, account, "r1",
					interfaces.VegaIndexKeys{PrimaryKeyFields: []string{"code"}})

				So(err, ShouldBeNil)
				So(changed, ShouldBeFalse)
				So(len(sent), ShouldEqual, 1)
			})

			Convey("A refused update is vega-backend's error\n", func() {
				replies[http.MethodPut] = reply{http.StatusConflict, `{"error_code":"VegaBackend.Resource.UpdateConflict"}`}

				_, err := each.UpdateIndexConfig(ctx, account, "r1", keys)

				var vegaErr *interfaces.VegaError
				So(errors.As(err, &vegaErr), ShouldBeTrue)
				So(vegaErr.HTTPCode, ShouldEqual, http.StatusConflict)
				So(vegaErr.ErrorCode, ShouldEqual, "VegaBackend.Resource.UpdateConflict")
			})

			Convey("A table the account may not read is vega-backend's error, and nothing is written\n", func() {
				replies[http.MethodGet] = reply{http.StatusForbidden, `{"error_code":"Public.Forbidden"}`}

				_, err := each.UpdateIndexConfig(ctx, account, "r1", keys)

				var vegaErr *interfaces.VegaError
				So(errors.As(err, &vegaErr), ShouldBeTrue)
				So(vegaErr.HTTPCode, ShouldEqual, http.StatusForbidden)
				So(len(sent), ShouldEqual, 1)
			})
		})

		Convey("CreateSemanticTask fills only what is empty unless asked otherwise\n", func() {
			status, respBody = http.StatusCreated,
				`{"id":"s1","scope":"resource","catalog_id":"c1","resource_id":"r1","status":"pending","applied":false,"create_time":5}`

			task, err := va.CreateSemanticTask(ctx, account, "r1", "en-US", "")

			So(err, ShouldBeNil)
			So(task, ShouldResemble, &interfaces.VegaSemanticTask{ID: "s1", ResourceID: "r1", Status: "pending", CreateTime: 5})
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/semantic-understanding-tasks")
			So(got[0].accountID, ShouldEqual, "u1")
			So(got[0].body, ShouldResemble, map[string]any{
				"scope": "resource", "resource_id": "r1", "language": "en-US", "apply_mode": "fill_empty"})

			_, err = va.CreateSemanticTask(ctx, account, "r1", "en-US", interfaces.VEGA_SEMANTIC_APPLY_FORCE)

			So(err, ShouldBeNil)
			So(got[1].body["apply_mode"], ShouldEqual, "force")
		})

		Convey("CreateSemanticTask returns vega-backend's refusal\n", func() {
			status, respBody = http.StatusForbidden, `{"error_code":"Public.Forbidden"}`

			_, err := va.CreateSemanticTask(ctx, account, "r1", "zh-CN", interfaces.VEGA_SEMANTIC_APPLY_FILL_EMPTY)

			var vegaErr *interfaces.VegaError
			So(errors.As(err, &vegaErr), ShouldBeTrue)
			So(vegaErr.HTTPCode, ShouldEqual, http.StatusForbidden)
		})

		Convey("ListSemanticTasks lists a catalog's table tasks only for an account that may manage them\n", func() {
			type reply struct {
				code int
				body string
			}
			tasksBody := `{"entries":[{"id":"s1","scope":"resource","resource_id":"r1","status":"completed","applied":true,
				"apply_mode":"fill_empty","create_time":5,"finish_time":9}],"total_count":1}`
			byPath := map[string]reply{
				"/api/vega-backend/in/v1/catalogs/c1":                  {http.StatusOK, `{"entries":[{"id":"c1","operations":["task_manage"]}]}`},
				"/api/vega-backend/in/v1/catalogs/c2":                  {http.StatusOK, `{"entries":[{"id":"c2","operations":["view_detail"]}]}`},
				"/api/vega-backend/in/v1/semantic-understanding-tasks": {http.StatusOK, tasksBody},
			}
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/vega-backend/in/v1/semantic-understanding-tasks" {
					queries = append(queries, r.URL.RawQuery)
				}
				w.WriteHeader(byPath[r.URL.Path].code)
				_, _ = w.Write([]byte(byPath[r.URL.Path].body))
			}))
			defer server.Close()
			each := &vegaAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/vega-backend/in/v1"}

			tasks, visible, err := each.ListSemanticTasks(ctx, account, "c1")
			So(err, ShouldBeNil)
			So(visible, ShouldBeTrue)
			So(tasks, ShouldResemble, []*interfaces.VegaSemanticTask{
				{ID: "s1", ResourceID: "r1", Status: "completed", Applied: true, ApplyMode: "fill_empty", CreateTime: 5, FinishTime: 9}})
			So(queries, ShouldResemble, []string{"catalog_id=c1&limit=-1&scope=resource"})

			// Without task_manage vega-backend would list nothing, which must not read as no task.
			tasks, visible, err = each.ListSemanticTasks(ctx, account, "c2")
			So(err, ShouldBeNil)
			So(visible, ShouldBeFalse)
			So(tasks, ShouldBeEmpty)
			So(len(queries), ShouldEqual, 1)
		})

		Convey("CreateBuildTask creates a full batch build of the table as the account\n", func() {
			status, respBody = http.StatusCreated, `{"id":"t9"}`

			taskID, err := va.CreateBuildTask(ctx, account, "r1")

			So(err, ShouldBeNil)
			So(taskID, ShouldEqual, "t9")
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/vega-backend/in/v1/build-tasks")
			So(got[0].accountID, ShouldEqual, "u1")
			So(got[0].body, ShouldResemble, map[string]any{"resource_id": "r1", "mode": "batch", "execute_type": "full"})
		})

		Convey("CreateBuildTask returns vega-backend's refusal with its code and details\n", func() {
			status, respBody = http.StatusBadRequest,
				`{"error_code":"VegaBackend.BuildTask.Exist","description":"Build task exists","error_details":"an active task"}`

			_, err := va.CreateBuildTask(ctx, account, "r1")

			var vegaErr *interfaces.VegaError
			So(errors.As(err, &vegaErr), ShouldBeTrue)
			So(vegaErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(vegaErr.ErrorCode, ShouldEqual, "VegaBackend.BuildTask.Exist")
			So(vegaErr.ErrorDetails, ShouldEqual, "an active task")
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
			So(rows[0]["total"], ShouldEqual, json.Number("800"))
		})

		Convey("Query keeps numbers as written, so a large identity is not rounded\n", func() {
			respBody = `{"entries":[{"id":1234567890123456789,"rate":0.1}]}`

			rows, err := va.Query(ctx, account, "SELECT id FROM {{r1}}", 60)

			So(err, ShouldBeNil)
			So(rows[0]["id"], ShouldEqual, json.Number("1234567890123456789"))
			So(rows[0]["rate"], ShouldEqual, json.Number("0.1"))
		})

		Convey("A vega-backend error keeps its status and code\n", func() {
			status, respBody = http.StatusNotImplemented, `{"error_code":"VegaBackend.Query.MultiCatalogNotSupported"}`

			_, err := va.Query(ctx, account, "SELECT 1", 60)

			var vegaErr *interfaces.VegaError
			So(errors.As(err, &vegaErr), ShouldBeTrue)
			So(vegaErr.HTTPCode, ShouldEqual, http.StatusNotImplemented)
			So(vegaErr.ErrorCode, ShouldEqual, "VegaBackend.Query.MultiCatalogNotSupported")
		})
	})
}
