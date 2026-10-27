// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"archive/tar"
	"bytes"
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

const token = "Bearer test-token"

type tarEntry struct {
	name     string
	typeflag byte
	content  string
}

func tarFixture(entries ...tarEntry) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		_ = tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0644, Size: int64(len(e.content))})
		_, _ = tw.Write([]byte(e.content))
	}
	_ = tw.Close()
	return buf.Bytes()
}

func readTar(data []byte) map[string]string {
	files := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := tr.Next()
		if err != nil {
			return files
		}
		content, _ := io.ReadAll(tr)
		files[header.Name] = string(content)
	}
}

func Test_BknBackendAccess_ExportBKN(t *testing.T) {
	Convey("Test bknBackendAccess ExportBKN\n", t, func() {
		var gotPath, gotBranch, gotAuth string
		reply := func(w http.ResponseWriter) {}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotBranch, gotAuth = r.URL.Path, r.URL.Query().Get("branch"), r.Header.Get("Authorization")
			reply(w)
		}))
		defer server.Close()
		bba := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
		ctx := context.Background()

		Convey("The tar files come back keyed by normalised path, without CHECKSUM\n", func() {
			reply = func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(tarFixture(
					tarEntry{name: "./", typeflag: tar.TypeDir},
					tarEntry{name: "./network.bkn", typeflag: tar.TypeReg, content: "network"},
					tarEntry{name: "object_types/", typeflag: tar.TypeDir},
					tarEntry{name: "object_types/order.bkn", typeflag: tar.TypeReg, content: "order"},
					tarEntry{name: "SKILL.md", typeflag: tar.TypeReg, content: "skill"},
					tarEntry{name: "CHECKSUM", typeflag: tar.TypeReg, content: "# generated: now"},
				))
			}

			files, err := bba.ExportBKN(ctx, token, "orders")

			So(err, ShouldBeNil)
			So(gotPath, ShouldEqual, "/api/bkn-backend/v1/bkns/orders")
			So(gotBranch, ShouldEqual, "main")
			So(gotAuth, ShouldEqual, token)
			So(files, ShouldResemble, map[string][]byte{
				"network.bkn":            []byte("network"),
				"object_types/order.bkn": []byte("order"),
				"SKILL.md":               []byte("skill"),
			})
		})

		Convey("An entry that escapes the archive root is refused\n", func() {
			reply = func(w http.ResponseWriter) {
				_, _ = w.Write(tarFixture(tarEntry{name: "../evil.bkn", typeflag: tar.TypeReg, content: "x"}))
			}

			_, err := bba.ExportBKN(ctx, token, "orders")

			So(err, ShouldNotBeNil)
		})

		Convey("A bkn-backend error keeps its status and error code\n", func() {
			reply = func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error_code":"BknBackend.KnowledgeNetwork.Forbidden","description":"forbidden"}`))
			}

			_, err := bba.ExportBKN(ctx, token, "orders")

			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusForbidden)
			So(backendErr.ErrorCode, ShouldEqual, "BknBackend.KnowledgeNetwork.Forbidden")
		})
	})
}

func Test_BknBackendAccess_ImportBKN(t *testing.T) {
	Convey("Test bknBackendAccess ImportBKN\n", t, func() {
		var gotPath, gotMode, gotBranch, gotAuth, gotFileType string
		var gotFiles map[string]string
		respBody := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
			gotMode, gotBranch = r.URL.Query().Get("import_mode"), r.URL.Query().Get("branch")
			file, header, err := r.FormFile("file")
			if err == nil {
				gotFileType = header.Header.Get("Content-Type")
				data, _ := io.ReadAll(file)
				gotFiles = readTar(data)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		bba := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
		ctx := context.Background()
		files := map[string][]byte{
			"network.bkn":            []byte("network"),
			"object_types/order.bkn": []byte("order"),
		}

		Convey("The files go up as a tar with overwrite on main\n", func() {
			respBody = `{"kn_id":"orders_1-0-0","capabilities":{"bound":2,"skipped":[]}}`

			result, err := bba.ImportBKN(ctx, token, files)

			So(err, ShouldBeNil)
			So(gotPath, ShouldEqual, "/api/bkn-backend/v1/bkns")
			So(gotMode, ShouldEqual, "overwrite")
			So(gotBranch, ShouldEqual, "main")
			So(gotAuth, ShouldEqual, token)
			So(gotFileType, ShouldEqual, "application/octet-stream")
			So(gotFiles, ShouldResemble, map[string]string{"network.bkn": "network", "object_types/order.bkn": "order"})
			So(result.KNID, ShouldEqual, "orders_1-0-0")
			So(result.Capabilities.Bound, ShouldEqual, 2)
			So(result.Capabilities.Skipped, ShouldBeEmpty)
		})

		Convey("Skipped capabilities are returned for the caller to fail on\n", func() {
			respBody = `{"kn_id":"orders_1-0-0","capabilities":{"bound":0,"skipped":[` +
				`{"capability_type":"skill","name":"refund","declared_id":"s1","reason":"not_usable","detail":"no execute"}]}}`

			result, err := bba.ImportBKN(ctx, token, files)

			So(err, ShouldBeNil)
			So(result.Capabilities.Skipped, ShouldResemble, []*interfaces.CapabilitySkip{{
				CapabilityType: "skill", Name: "refund", DeclaredID: "s1", Reason: "not_usable", Detail: "no execute",
			}})
		})
	})
}

func Test_BknBackendAccess_Schedules(t *testing.T) {
	Convey("Test bknBackendAccess schedule calls\n", t, func() {
		type request struct {
			method, path, query, auth, contentType string
			body                                   map[string]any
		}
		var got []request
		status, respBody := http.StatusOK, ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
				auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type")}
			if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		bba := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
		ctx := context.Background()
		const schedulesPath = "/api/bkn-backend/v1/knowledge-networks/orders_v1_0_0/action-schedules"

		Convey("ListSchedules reads every schedule of main in one page\n", func() {
			respBody = `{"entries":[{"id":"s1","name":"nightly","kn_id":"orders_v1_0_0","action_type_id":"restock",` +
				`"cron_expression":"0 2 * * *","_instance_identities":[{"id":"p1"}],"status":"active"}],"total_count":1}`

			schedules, err := bba.ListSchedules(ctx, token, "orders_v1_0_0")

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodGet)
			So(got[0].path, ShouldEqual, schedulesPath)
			So(got[0].query, ShouldEqual, "branch=main&limit=-1")
			So(got[0].auth, ShouldEqual, token)
			So(schedules, ShouldResemble, []*interfaces.ActionSchedule{{
				ID: "s1", Name: "nightly", ActionTypeID: "restock", CronExpression: "0 2 * * *",
				InstanceIdentities: []map[string]any{{"id": "p1"}}, Status: "active",
			}})
		})

		Convey("CreateSchedule posts the schedule without its id and returns the new one\n", func() {
			status, respBody = http.StatusCreated, `{"id":"s9"}`

			id, err := bba.CreateSchedule(ctx, token, "orders_v1_0_0", &interfaces.ActionSchedule{
				ID: "s1", Name: "nightly", ActionTypeID: "restock", CronExpression: "0 2 * * *",
				InstanceIdentities: []map[string]any{{"id": "p1"}}, DynamicParams: map[string]any{"qty": float64(3)},
				Status: interfaces.SCHEDULE_STATUS_ACTIVE,
			})

			So(err, ShouldBeNil)
			So(id, ShouldEqual, "s9")
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, schedulesPath)
			So(got[0].query, ShouldEqual, "branch=main")
			So(got[0].contentType, ShouldEqual, "application/json")
			So(got[0].body, ShouldResemble, map[string]any{
				"name": "nightly", "action_type_id": "restock", "cron_expression": "0 2 * * *",
				"_instance_identities": []any{map[string]any{"id": "p1"}}, "dynamic_params": map[string]any{"qty": float64(3)},
				"status": "active",
			})
		})

		Convey("SetScheduleStatus puts the status\n", func() {
			err := bba.SetScheduleStatus(ctx, token, "orders_v1_0_0", "s1", interfaces.SCHEDULE_STATUS_INACTIVE)

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPut)
			So(got[0].path, ShouldEqual, schedulesPath+"/s1/status")
			So(got[0].body, ShouldResemble, map[string]any{"status": "inactive"})
		})

		Convey("A bkn-backend error keeps its status and error code\n", func() {
			status, respBody = http.StatusForbidden, `{"error_code":"BknBackend.ActionSchedule.Forbidden"}`

			_, err := bba.CreateSchedule(ctx, token, "orders_v1_0_0", &interfaces.ActionSchedule{Name: "nightly"})

			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusForbidden)
			So(backendErr.ErrorCode, ShouldEqual, "BknBackend.ActionSchedule.Forbidden")
		})
	})
}

func Test_BknBackendAccess_Networks(t *testing.T) {
	Convey("Test bknBackendAccess network calls\n", t, func() {
		type request struct {
			method, path, query, auth string
			body                      map[string]any
		}
		var got []request
		status, respBody := http.StatusOK, ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
			if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		bba := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
		ctx := context.Background()
		const networksPath = "/api/bkn-backend/v1/knowledge-networks"

		Convey("CreateKN posts the id and name and returns the id bkn-backend gave\n", func() {
			status, respBody = http.StatusCreated, `{"id":"01a1-generated"}`

			id, err := bba.CreateKN(ctx, token, "", "Orders")

			So(err, ShouldBeNil)
			So(id, ShouldEqual, "01a1-generated")
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, networksPath)
			So(got[0].auth, ShouldEqual, token)
			So(got[0].body, ShouldResemble, map[string]any{"id": "", "name": "Orders"})
		})

		Convey("CreateKN without an id in the reply fails\n", func() {
			status, respBody = http.StatusCreated, `{}`

			_, err := bba.CreateKN(ctx, token, "orders", "Orders")

			So(err, ShouldNotBeNil)
		})

		Convey("GetKN reads the network of main\n", func() {
			respBody = `{"id":"orders","name":"Orders","branch":"main"}`

			network, err := bba.GetKN(ctx, token, "orders")

			So(err, ShouldBeNil)
			So(network, ShouldResemble, &interfaces.KnowledgeNetwork{ID: "orders", Name: "Orders"})
			So(got[0].method, ShouldEqual, http.MethodGet)
			So(got[0].path, ShouldEqual, networksPath+"/orders")
			So(got[0].query, ShouldEqual, "branch=main")
		})

		Convey("ListKNIDs reads every network the caller can see in one page\n", func() {
			respBody = `{"entries":[{"id":"orders","name":"Orders"},{"id":"parts","name":"Parts"}],"total_count":2}`

			ids, err := bba.ListKNIDs(ctx, token)

			So(err, ShouldBeNil)
			So(ids, ShouldResemble, []string{"orders", "parts"})
			So(got[0].path, ShouldEqual, networksPath)
			So(got[0].query, ShouldEqual, "limit=-1")
		})

		Convey("ListKNIDs of no network is empty\n", func() {
			respBody = `{"entries":null,"total_count":0}`

			ids, err := bba.ListKNIDs(ctx, token)

			So(err, ShouldBeNil)
			So(ids, ShouldNotBeNil)
			So(ids, ShouldBeEmpty)
		})

		Convey("A bkn-backend error keeps its status and error code\n", func() {
			status, respBody = http.StatusForbidden, `{"error_code":"Public.Forbidden"}`

			_, err := bba.GetKN(ctx, token, "orders")

			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusForbidden)
			So(backendErr.ErrorCode, ShouldEqual, "Public.Forbidden")
		})
	})
}
