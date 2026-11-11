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
