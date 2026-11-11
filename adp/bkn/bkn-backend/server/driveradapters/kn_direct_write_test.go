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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// publishingVersions is a versioned network at running version 2 that records publishes and
// draft resets.
type publishingVersions struct {
	interfaces.KNVersionService
	publishErr error
	published  []*interfaces.KNPublishRequest
	reset      []*interfaces.KNResetDraftRequest
	// ctxErrs records whether each publish and reset ran on a context already done.
	ctxErrs []error
	rebased int
}

func (v *publishingVersions) RebaseDraft(context.Context, string) error {
	v.rebased++
	return nil
}

func (v *publishingVersions) ResolveBranch(_ context.Context, _ string, _ interfaces.KNBranchRequest) (*interfaces.KNResolvedBranch, error) {
	return &interfaces.KNResolvedBranch{Branch: interfaces.DRAFT_BRANCH, Versioned: true, RunningVersion: 2}, nil
}

func (v *publishingVersions) Publish(ctx context.Context, _ string, req *interfaces.KNPublishRequest) (*interfaces.KNPublishResult, error) {
	v.published = append(v.published, req)
	v.ctxErrs = append(v.ctxErrs, ctx.Err())
	if v.publishErr != nil {
		return nil, v.publishErr
	}
	return &interfaces.KNPublishResult{KNVersion: &interfaces.KNVersion{Version: 3}}, nil
}

func (v *publishingVersions) ResetDraft(ctx context.Context, _ string, req *interfaces.KNResetDraftRequest) (*interfaces.KNVersioningView, error) {
	v.reset = append(v.reset, req)
	v.ctxErrs = append(v.ctxErrs, ctx.Err())
	return &interfaces.KNVersioningView{}, nil
}

// serveDirectWrite runs one PUT through a handler that writes the draft at revision 7 and
// replies with status.
func serveDirectWrite(t *testing.T, kvs *publishingVersions, query string, written bool, status int) *httptest.ResponseRecorder {
	t.Helper()
	return serveDirectWriteGone(t, kvs, query, written, status, false)
}

// serveDirectWriteGone is serveDirectWrite with a client that may have gone before the handler
// returned.
func serveDirectWriteGone(t *testing.T, kvs *publishingVersions, query string, written bool, status int, gone bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &restHandler{kvs: kvs}
	engine := gin.New()
	engine.Use(r.finishDirectWrites())
	engine.PUT("/kn/:kn_id/object-types/:ot_id", func(c *gin.Context) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if _, ok := r.writeBranch(&ctx, c, c.Param("kn_id")); !ok {
			return
		}
		if gone {
			cancel()
		}
		if written {
			dw := logics.DraftWriteFrom(ctx)
			dw.Written, dw.Revision = true, 7
		}
		c.Header("ETag", `"r2"`)
		c.JSON(status, gin.H{"id": c.Param("ot_id")})
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/kn/kn1/object-types/ot1"+query, nil))
	return recorder
}

func TestDirectWriteIsPublished(t *testing.T) {
	kvs := &publishingVersions{}
	recorder := serveDirectWrite(t, kvs, "", true, http.StatusOK)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"id":"ot1"}` {
		t.Fatalf("response %d %s", recorder.Code, recorder.Body.String())
	}
	if len(kvs.published) != 1 || len(kvs.reset) != 0 {
		t.Fatalf("published %d reset %d", len(kvs.published), len(kvs.reset))
	}
	req := kvs.published[0]
	if *req.ExpectedRunningVersion != 2 || *req.ExpectedDraftRevision != 7 {
		t.Fatalf("publish expects running %d revision %d", *req.ExpectedRunningVersion, *req.ExpectedDraftRevision)
	}
	if kvs.rebased != 1 {
		t.Fatalf("draft rebased %d times before the write", kvs.rebased)
	}
	if got := recorder.Header().Get("X-Kn-Version"); got != "3" {
		t.Fatalf("X-Kn-Version %q", got)
	}
	if got := recorder.Header().Get("ETag"); got != `"r2"` {
		t.Fatalf("handler header lost: ETag %q", got)
	}
}

// A client that gives up waiting cancels its request; the write it made is published, or put
// back, all the same, so the draft is not left dirty for every later direct write.
func TestDirectWriteFinishesWhenTheClientHasGone(t *testing.T) {
	kvs := &publishingVersions{}
	serveDirectWriteGone(t, kvs, "", true, http.StatusOK, true)
	if len(kvs.published) != 1 || kvs.ctxErrs[0] != nil {
		t.Fatalf("published %d on %v", len(kvs.published), kvs.ctxErrs)
	}

	kvs = &publishingVersions{publishErr: httperrors.NewHTTPError(context.Background(), http.StatusBadRequest,
		berrors.BknBackend_KNVersion_ValidationFailed)}
	serveDirectWriteGone(t, kvs, "", true, http.StatusOK, true)
	if len(kvs.reset) != 1 || kvs.ctxErrs[1] != nil {
		t.Fatalf("reset %d on %v", len(kvs.reset), kvs.ctxErrs)
	}
}

func TestDraftWriteIsNotPublished(t *testing.T) {
	kvs := &publishingVersions{}
	recorder := serveDirectWrite(t, kvs, "?branch=draft", true, http.StatusOK)
	// A write naming the draft still needs If-Match.
	if recorder.Code != http.StatusPreconditionRequired || len(kvs.published) != 0 || kvs.rebased != 0 {
		t.Fatalf("response %d, published %d, rebased %d", recorder.Code, len(kvs.published), kvs.rebased)
	}
}

func TestDirectWriteWithoutChangesPublishesNothing(t *testing.T) {
	kvs := &publishingVersions{}
	recorder := serveDirectWrite(t, kvs, "", false, http.StatusOK)
	if recorder.Code != http.StatusOK || len(kvs.published) != 0 || len(kvs.reset) != 0 {
		t.Fatalf("response %d, published %d reset %d", recorder.Code, len(kvs.published), len(kvs.reset))
	}
}

func TestDirectWriteNothingToPublishKeepsResponse(t *testing.T) {
	kvs := &publishingVersions{publishErr: httperrors.NewHTTPError(context.Background(), http.StatusConflict,
		berrors.BknBackend_KNVersion_NothingToPublish)}
	recorder := serveDirectWrite(t, kvs, "", true, http.StatusOK)
	if recorder.Code != http.StatusOK || len(kvs.reset) != 0 || recorder.Header().Get("X-Kn-Version") != "" {
		t.Fatalf("response %d, reset %d, version %q", recorder.Code, len(kvs.reset), recorder.Header().Get("X-Kn-Version"))
	}
}

func TestDirectWriteFailedPublishDiscardsTheWrite(t *testing.T) {
	kvs := &publishingVersions{publishErr: httperrors.NewHTTPError(context.Background(), http.StatusBadRequest,
		berrors.BknBackend_KNVersion_ValidationFailed)}
	recorder := serveDirectWrite(t, kvs, "", true, http.StatusOK)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("response %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("ETag") != "" {
		t.Fatal("the refused write still carries the handler's ETag")
	}
	if len(kvs.reset) != 1 || *kvs.reset[0].ExpectedDraftRevision != 7 || !kvs.reset[0].OverwriteChanges {
		t.Fatalf("reset %+v", kvs.reset)
	}
}

func TestDirectWriteFailedHandlerDiscardsTheWrite(t *testing.T) {
	kvs := &publishingVersions{}
	recorder := serveDirectWrite(t, kvs, "", true, http.StatusInternalServerError)
	if recorder.Code != http.StatusInternalServerError || len(kvs.published) != 0 || len(kvs.reset) != 1 {
		t.Fatalf("response %d, published %d reset %d", recorder.Code, len(kvs.published), len(kvs.reset))
	}
}
