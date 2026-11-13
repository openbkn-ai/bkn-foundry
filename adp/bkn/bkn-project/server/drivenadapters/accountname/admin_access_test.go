// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package accountname

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_IsAdmin(t *testing.T) {
	Convey("IsAdmin reads the caller's roles from bkn-safe with the caller's token\n", t, func() {
		body, status, auth, path := `{"enabled":true,"roles":["normal","admin"]}`, http.StatusOK, "", ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			path = r.URL.Path
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()
		access := NewAdminAccessAt(server.URL + "/")
		ctx := context.Background()

		So(access.IsAdmin(ctx, "Bearer t"), ShouldBeTrue)
		So(auth, ShouldEqual, "Bearer t")
		So(path, ShouldEqual, "/api/safe/v1/me")

		body = `{"enabled":true,"roles":["super_admin"]}`
		So(access.IsAdmin(ctx, "Bearer t"), ShouldBeTrue)

		body = `{"enabled":true,"roles":["network_builder"]}`
		So(access.IsAdmin(ctx, "Bearer t"), ShouldBeFalse)

		body = `{"enabled":false,"roles":["admin"]}`
		So(access.IsAdmin(ctx, "Bearer t"), ShouldBeFalse)

		body, status = `{"enabled":true,"roles":["admin"]}`, http.StatusUnauthorized
		So(access.IsAdmin(ctx, "Bearer t"), ShouldBeFalse)

		So(access.IsAdmin(ctx, ""), ShouldBeFalse)
		So(NewAdminAccessAt("").IsAdmin(ctx, "Bearer t"), ShouldBeFalse)
	})
}
