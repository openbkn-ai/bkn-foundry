// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/permissionrequest"
)

type permissionRequestLivenessRecorder struct{ calls int }

func (r *permissionRequestLivenessRecorder) Exists(context.Context, string, string) (bool, error) {
	r.calls++
	return false, nil
}

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

func TestCreatePermissionRequestRejectsResourceControlCharacters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, field := range []string{"id", "name"} {
		for _, control := range []string{"\x00", "\r", "\n"} {
			for _, position := range []string{"leading", "middle", "trailing"} {
				t.Run(field+"/"+control+"/"+position, func(t *testing.T) {
					value := "resource"
					switch position {
					case "leading":
						value = control + value
					case "middle":
						value = "res" + control + "ource"
					case "trailing":
						value += control
					}
					resource := map[string]string{"type": "resource", "id": "resource-id", "name": "resource name"}
					resource[field] = value
					payload, err := json.Marshal(map[string]any{
						"resource": resource, "operations": []string{"view_detail"}, "proposal": map[string]string{"kind": "grant"}, "reason": "need access",
					})
					if err != nil {
						t.Fatalf("marshal request: %v", err)
					}

					router := gin.New()
					router.Use(func(c *gin.Context) { c.Set(ctxAccessorID, "requester") })
					liveness := &permissionRequestLivenessRecorder{}
					registerPublicPermissionRequests(router.Group("/api/safe/v1"), permissionrequest.New(nil, nil, liveness))
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/safe/v1/permission-requests", bytes.NewReader(payload)))
					if response.Code != http.StatusBadRequest {
						t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
					}
					if liveness.calls != 0 {
						t.Fatalf("resource liveness checks = %d, want 0", liveness.calls)
					}
				})
			}
		}
	}
}
