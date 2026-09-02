// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"bkn-backend/common"
	padapter "bkn-backend/drivenadapters/permission"
	"bkn-backend/interfaces"
)

// Test_PermissionServiceImpl_FilterResources_FullOperationSet wires the service
// to the real bkn-safe adapter against a stub bkn-safe, and asserts the whole
// chain the knowledge-network list and detail responses depend on:
//
//   - the candidate operation set (COMMON_OPERATIONS) reaches bkn-safe instead
//     of being dropped at the service layer;
//   - view_detail, modify and delete come back together for an authorized
//     resource, which is what Studio reads to decide whether to show the
//     edit/delete entry points;
//   - the page costs one request, not one per resource per operation.
func Test_PermissionServiceImpl_FilterResources_FullOperationSet(t *testing.T) {
	Convey("Test FilterResources returns the full allowed operation set\n", t, func() {
		// Assertions stay out of the handler: goconvey's So panics when called
		// off the Convey goroutine, which would surface as a connection error
		// rather than a test failure.
		var requests []map[string]any
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			requests = append(requests, req)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resources": []map[string]any{
					{
						"resource_type": interfaces.RESOURCE_TYPE_KN,
						"resource_id":   "kn1",
						"operations":    []string{"view_detail", "modify", "delete"},
					},
				},
			})
		}))
		defer srv.Close()

		svc := &PermissionServiceImpl{
			appSetting: &common.AppSetting{},
			mqClient:   &mockMQClient{},
			pa:         padapter.NewPermissionAccess(srv.URL),
		}

		ctx := withAccountInfo(context.Background(), "u1", "user")
		result, err := svc.FilterResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{"kn1", "kn2"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)

		So(err, ShouldBeNil)
		So(paths, ShouldResemble, []string{"/api/safe/v1/authz/resource-filter"})
		So(len(requests), ShouldEqual, 1)

		candidates, _ := requests[0]["candidate_operations"].([]any)
		So(len(candidates), ShouldEqual, len(interfaces.COMMON_OPERATIONS))
		So(candidates[0], ShouldEqual, interfaces.OPERATION_TYPE_VIEW_DETAIL)
		visibility, _ := requests[0]["visibility_operations"].([]any)
		So(len(visibility), ShouldEqual, 1)

		// kn2 is absent from the reply: not visible, so it is filtered out.
		So(len(result), ShouldEqual, 1)
		So(result["kn1"].Operations, ShouldResemble, []string{"view_detail", "modify", "delete"})
	})
}
