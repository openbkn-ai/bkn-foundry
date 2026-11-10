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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// resolvingVersions resolves every write to the draft, as for a versioned network or not.
type resolvingVersions struct {
	interfaces.KNVersionService
	versioned bool
}

func (v resolvingVersions) ResolveBranch(_ context.Context, _ string, req interfaces.KNBranchRequest) (*interfaces.KNResolvedBranch, error) {
	if !v.versioned {
		return &interfaces.KNResolvedBranch{Branch: interfaces.MAIN_BRANCH}, nil
	}
	return &interfaces.KNResolvedBranch{Branch: interfaces.DRAFT_BRANCH, Versioned: true}, nil
}

func TestResolveWritePreconditions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name      string
		versioned bool
		need      writePrecondition
		query     string
		ifMatch   string
		wantCode  int
	}{
		{"create needs nothing", true, preconditionNone, "", "", 0},
		{"change without If-Match", true, preconditionIfMatch, "", "", http.StatusPreconditionRequired},
		{"change with If-Match", true, preconditionIfMatch, "", `"r1"`, 0},
		{"change with only the draft revision", true, preconditionIfMatch, "expected_draft_revision=3", "", http.StatusPreconditionRequired},
		{"delete without either", true, preconditionDelete, "", "", http.StatusPreconditionRequired},
		{"delete with If-Match", true, preconditionDelete, "", "r1", 0},
		{"delete several with the draft revision", true, preconditionDelete, "expected_draft_revision=3", "", 0},
		{"import without the draft revision", true, preconditionDraftRevision, "", "r1", http.StatusPreconditionRequired},
		{"import with the draft revision", true, preconditionDraftRevision, "expected_draft_revision=3", "", 0},
		{"unversioned change needs nothing", false, preconditionIfMatch, "", "", 0},
		{"unversioned import needs nothing", false, preconditionDraftRevision, "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPut, "/kn?"+tc.query, nil)
			if tc.ifMatch != "" {
				c.Request.Header.Set("If-Match", tc.ifMatch)
			}
			r := &restHandler{kvs: resolvingVersions{versioned: tc.versioned}}
			ctx := context.Background()
			_, ok := r.resolveWrite(&ctx, c, "kn1", tc.need)
			if tc.wantCode != 0 {
				if ok || recorder.Code != tc.wantCode {
					t.Fatalf("ok %v code %d, want %d", ok, recorder.Code, tc.wantCode)
				}
				return
			}
			if !ok {
				t.Fatalf("refused with %d: %s", recorder.Code, recorder.Body.String())
			}
			dw := logics.DraftWriteFrom(ctx)
			if dw == nil || (tc.ifMatch != "" && dw.IfMatch != "r1") {
				t.Fatalf("draft write in ctx: %+v", dw)
			}
		})
	}
}

func TestPreconditionByMethod(t *testing.T) {
	for method, want := range map[string]writePrecondition{
		http.MethodPost: preconditionNone, http.MethodPut: preconditionIfMatch, http.MethodDelete: preconditionDelete,
	} {
		if got := preconditionByMethod(method); got != want {
			t.Errorf("%s: %v, want %v", method, got, want)
		}
	}
}

func TestIfMatchRevision(t *testing.T) {
	for header, want := range map[string]string{`"r1"`: "r1", `W/"r1"`: "r1", " r1 ": "r1", "": ""} {
		if got := ifMatchRevision(header); got != want {
			t.Errorf("%q: %q, want %q", header, got, want)
		}
	}
}
