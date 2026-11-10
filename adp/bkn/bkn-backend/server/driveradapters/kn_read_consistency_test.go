// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// endChecks answers CheckReadEnd from a queue; an empty queue means the read was whole.
type endChecks struct {
	interfaces.KNVersionService
	codes []string
}

func (e *endChecks) CheckReadEnd(ctx context.Context, _ string, _ *interfaces.KNResolvedBranch) error {
	if len(e.codes) == 0 {
		return nil
	}
	code := e.codes[0]
	e.codes = e.codes[1:]
	status := http.StatusConflict
	if code == berrors.BknBackend_KNVersion_VersionPurged {
		status = http.StatusGone
	}
	return httperrors.NewHTTPError(ctx, status, code)
}

func TestConsistentReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mismatch, purged := berrors.BknBackend_KNVersion_DraftRevisionMismatch, berrors.BknBackend_KNVersion_VersionPurged
	cases := []struct {
		name     string
		marked   bool
		codes    []string
		wantCode int
		wantRuns int
	}{
		{"unmarked read is not checked", false, []string{purged}, http.StatusOK, 1},
		{"whole read", true, nil, http.StatusOK, 1},
		{"draft written once is read again", true, []string{mismatch}, http.StatusOK, 2},
		{"draft written twice", true, []string{mismatch, mismatch}, http.StatusConflict, 2},
		{"version purged", true, []string{purged}, http.StatusGone, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &restHandler{kvs: &endChecks{codes: tc.codes}}
			runs := 0
			engine := gin.New()
			engine.Use(r.consistentReads())
			engine.GET("/kn/:kn_id", func(c *gin.Context) {
				runs++
				if tc.marked {
					markVersionedRead(c, "kn1", &interfaces.KNResolvedBranch{Branch: "draft", Versioned: true, Version: "draft"})
				}
				c.JSON(http.StatusOK, gin.H{"run": runs})
			})
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/kn/kn1", nil))
			if recorder.Code != tc.wantCode || runs != tc.wantRuns {
				t.Fatalf("code %d runs %d body %s, want %d %d", recorder.Code, runs, recorder.Body.String(), tc.wantCode, tc.wantRuns)
			}
			if tc.wantCode == http.StatusOK && recorder.Body.String() != `{"run":`+string(rune('0'+runs))+`}` {
				t.Fatalf("body %s: only the last run is sent", recorder.Body.String())
			}
		})
	}
}
