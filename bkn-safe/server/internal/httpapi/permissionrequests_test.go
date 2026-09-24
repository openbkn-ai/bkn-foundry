// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/permissionrequest"
)

func TestWritePermissionRequestErrorExposesConflictReason(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		name   string
		err    error
		reason string
	}{
		{name: "permission already granted", err: permissionrequest.ErrPermissionAlreadyGranted, reason: "permission_already_granted"},
		{name: "missing prerequisite", err: permissionrequest.ErrPrerequisiteMissing, reason: "missing_prerequisite"},
		{name: "resource deleted", err: permissionrequest.ErrResourceDeleted, reason: "resource_deleted"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/permission-requests", func(c *gin.Context) {
				if !writePermissionRequestError(c, testCase.err) {
					t.Fatal("writePermissionRequestError returned false")
				}
			})

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/permission-requests", nil))
			if response.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
			}

			var body struct {
				ErrorCode    string         `json:"error_code"`
				ErrorDetails map[string]any `json:"error_details"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.ErrorCode != "BknSafe.Conflict" {
				t.Errorf("error_code = %q, want BknSafe.Conflict", body.ErrorCode)
			}
			if body.ErrorDetails["reason"] != testCase.reason {
				t.Errorf("error_details.reason = %q, want %q", body.ErrorDetails["reason"], testCase.reason)
			}
		})
	}
}
