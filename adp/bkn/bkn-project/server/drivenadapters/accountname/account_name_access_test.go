// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package accountname

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_AccountNames(t *testing.T) {
	Convey("Test naming accounts through bkn-safe\n", t, func() {
		var got map[string][]string
		var path string
		calls := 0
		status := http.StatusOK
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			path = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"user_names":[{"id":"u1","name":"Alice"},{"id":"u2","name":" "}],"app_names":[{"id":"a1","name":"Robot"}]}`))
		}))
		defer server.Close()
		access := NewAccountNameAccessAt(server.URL + "/")
		alice := interfaces.AccountInfo{ID: "u1", Type: "user"}
		blank := interfaces.AccountInfo{ID: "u2", Type: "user"}
		robot := interfaces.AccountInfo{ID: "a1", Type: "app"}

		Convey("users and apps are asked once each and named\n", func() {
			names := access.Names(context.Background(), []interfaces.AccountInfo{alice, alice, blank, robot, {ID: "x", Type: "other"}})
			So(path, ShouldEqual, "/api/safe/v1/directory/names")
			So(got, ShouldResemble, map[string][]string{"user_ids": {"u1", "u2"}, "app_ids": {"a1"}})
			So(names, ShouldResemble, map[interfaces.AccountInfo]string{alice: "Alice", robot: "Robot"})
		})
		Convey("a failed lookup names no one\n", func() {
			status = http.StatusInternalServerError
			So(access.Names(context.Background(), []interfaces.AccountInfo{alice}), ShouldBeEmpty)
		})
		Convey("nothing to name, or no bkn-safe, asks nothing\n", func() {
			So(access.Names(context.Background(), nil), ShouldBeEmpty)
			So(NewAccountNameAccessAt("").Names(context.Background(), []interfaces.AccountInfo{alice}), ShouldBeEmpty)
			So(calls, ShouldEqual, 0)
		})
	})
}
