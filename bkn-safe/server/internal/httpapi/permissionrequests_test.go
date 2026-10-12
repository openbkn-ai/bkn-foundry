// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permissionproposal"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/permissionrequest"
)

type permissionRequestLivenessRecorder struct{ calls int }

func (r *permissionRequestLivenessRecorder) Exists(context.Context, string, string) (bool, error) {
	r.calls++
	return false, nil
}

func TestWritePermissionRequestErrorMapsBusinessFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{name: "permission already granted", err: permissionrequest.ErrPermissionAlreadyGranted, status: http.StatusConflict, reason: "permission_already_granted"},
		{name: "missing prerequisite", err: permissionrequest.ErrPrerequisiteMissing, status: http.StatusConflict, reason: "missing_prerequisite"},
		{name: "resource deleted", err: permissionrequest.ErrResourceDeleted, status: http.StatusConflict, reason: "resource_deleted"},
		{name: "forbidden reviewer", err: permissionrequest.ErrForbidden, status: http.StatusForbidden},
		{name: "unlicensed proposal", err: permissionproposal.ErrUnavailable, status: http.StatusNotFound, reason: "permission_proposal_unavailable"},
		{name: "uninstalled proposal", err: permissionrequest.ErrProposalUnavailable, status: http.StatusNotFound, reason: "permission_proposal_unavailable"},
		{name: "unexpected preview failure", err: errors.New("preview failed"), status: http.StatusInternalServerError},
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
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d", response.Code, testCase.status)
			}

			var body struct {
				ErrorCode    string          `json:"error_code"`
				ErrorDetails json.RawMessage `json:"error_details"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if testCase.status == http.StatusConflict && body.ErrorCode != "BknSafe.Conflict" {
				t.Errorf("error_code = %q, want BknSafe.Conflict", body.ErrorCode)
			}
			if testCase.status == http.StatusInternalServerError && body.ErrorCode != "BknSafe.InternalError" {
				t.Errorf("error_code = %q, want BknSafe.InternalError", body.ErrorCode)
			}
			if testCase.reason != "" {
				var details map[string]any
				if err := json.Unmarshal(body.ErrorDetails, &details); err != nil {
					t.Fatalf("decode error details: %v", err)
				}
				if details["reason"] != testCase.reason {
					t.Errorf("error_details.reason = %q, want %q", details["reason"], testCase.reason)
				}
			}
		})
	}
}

func TestPermissionRequestProposalPreviewWithoutHandlerReturnsBusinessError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPermissionRequests(router.Group("/api/safe/v1/me"), nil)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/safe/v1/me/permission-requests/proposal-preview?resource_type=object_type&resource_id=network%2Fshipment", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	var body struct {
		ErrorDetails map[string]string `json:"error_details"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorDetails["reason"] != "permission_proposal_unavailable" {
		t.Fatalf("reason = %q, want permission_proposal_unavailable", body.ErrorDetails["reason"])
	}
}

func TestCreatePermissionRequestRejectsControlCharactersBeforeBusinessValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, field := range []string{"id", "name", "reason"} {
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
					reason := "need access"
					if field == "reason" {
						reason = value
					} else {
						resource[field] = value
					}
					if field == "reason" && (control == "\r" || control == "\n") {
						return
					}
					payload, err := json.Marshal(map[string]any{
						"resource": resource, "operations": []string{"view_detail"}, "proposal": map[string]string{"kind": "grant"}, "reason": reason,
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

func TestCreatePermissionRequestAllowsMultilineReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload, err := json.Marshal(map[string]any{
		"resource":   map[string]string{"type": "resource", "id": "resource-id", "name": "resource name"},
		"operations": []string{"view_detail"}, "proposal": map[string]string{"kind": "grant"},
		"reason": "222\n123",
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
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
	if liveness.calls != 1 {
		t.Fatalf("resource liveness checks = %d, want 1", liveness.calls)
	}
}
