// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// resolvingVersions resolves every write to the draft, as for a versioned network or not.
type resolvingVersions struct {
	interfaces.KNVersionService
	versioned bool
}

func (resolvingVersions) RebaseDraft(context.Context, string) error { return nil }

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
		{"create needs nothing", true, preconditionNone, "branch=draft", "", 0},
		{"change without If-Match", true, preconditionIfMatch, "branch=draft", "", http.StatusPreconditionRequired},
		{"change with If-Match", true, preconditionIfMatch, "branch=draft", `"r1"`, 0},
		{"change with only the draft revision", true, preconditionIfMatch, "branch=draft&expected_draft_revision=3", "", http.StatusPreconditionRequired},
		{"delete without either", true, preconditionDelete, "branch=draft", "", http.StatusPreconditionRequired},
		{"delete with If-Match", true, preconditionDelete, "branch=draft", "r1", 0},
		{"delete several with the draft revision", true, preconditionDelete, "branch=draft&expected_draft_revision=3", "", 0},
		{"import without the draft revision", true, preconditionDraftRevision, "branch=draft", "r1", http.StatusPreconditionRequired},
		{"import with the draft revision", true, preconditionDraftRevision, "branch=draft&expected_draft_revision=3", "", 0},
		// A write that does not name the draft is published at once and needs no precondition.
		{"direct change needs nothing", true, preconditionIfMatch, "", "", 0},
		{"direct change on main needs nothing", true, preconditionIfMatch, "branch=main", "", 0},
		{"direct delete needs nothing", true, preconditionDelete, "", "", 0},
		{"direct import needs nothing", true, preconditionDraftRevision, "", "", 0},
		{"direct change keeps its If-Match", true, preconditionIfMatch, "", `"r1"`, 0},
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
			_, direct := c.Get(directWritesKey)
			wantDirect := tc.versioned && !strings.Contains(tc.query, "branch=draft")
			if direct != wantDirect || dw.RefuseDraftChanges != wantDirect {
				t.Fatalf("direct %v refuse draft changes %v, want %v", direct, dw.RefuseDraftChanges, wantDirect)
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

// Uploads take import_mode=replace as overwrite; JSON imports keep refusing it.
func TestParseImportOptionsReplace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		allowReplace bool
		wantMode     string
		wantErr      bool
	}{{true, interfaces.ImportMode_Overwrite, false}, {false, "", true}} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/bkns?import_mode=replace", nil)
		// The handler reads the query before parsing, as UploadBKN does.
		_ = c.Query(interfaces.QueryParam_ImportMode)
		mode, _, httpErr := parseKnowledgeNetworkImportOptions(context.Background(), c,
			interfaces.ImportMode_Overwrite, false, tc.allowReplace)
		if (httpErr != nil) != tc.wantErr || mode != tc.wantMode {
			t.Errorf("allowReplace %v: mode %q err %v", tc.allowReplace, mode, httpErr)
		}
	}
}

// knownVersions has versions 1 and 2, of which 1 was purged.
type knownVersions struct{ interfaces.KNVersionService }

func (knownVersions) ResolveBranch(ctx context.Context, _ string, req interfaces.KNBranchRequest) (*interfaces.KNResolvedBranch, error) {
	if req.KNVersion == "1" {
		return nil, httperrors.NewHTTPError(ctx, http.StatusGone, berrors.BknBackend_KNVersion_VersionPurged)
	}
	return &interfaces.KNResolvedBranch{Branch: "v" + req.KNVersion, Versioned: true, Version: req.KNVersion}, nil
}

func TestProxyBranchReadable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for query, want := range map[string]int{"": 0, "branch=main": 0, "branch=v2": 0,
		"branch=v1": http.StatusGone, "branch=draft": http.StatusBadRequest, "branch=vx": http.StatusBadRequest} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/resolve?"+query, nil)
		ok := (&restHandler{kvs: knownVersions{}}).proxyBranchReadable(context.Background(), c, "kn1")
		if (want == 0) != ok || (want != 0 && recorder.Code != want) {
			t.Errorf("%q: ok %v code %d, want %d", query, ok, recorder.Code, want)
		}
	}
}
