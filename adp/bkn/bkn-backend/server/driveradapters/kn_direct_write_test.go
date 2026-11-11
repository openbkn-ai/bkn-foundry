// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
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
}

func (v *publishingVersions) ResolveBranch(_ context.Context, _ string, _ interfaces.KNBranchRequest) (*interfaces.KNResolvedBranch, error) {
	return &interfaces.KNResolvedBranch{Branch: interfaces.DRAFT_BRANCH, Versioned: true, RunningVersion: 2}, nil
}

func (v *publishingVersions) Publish(_ context.Context, _ string, req *interfaces.KNPublishRequest) (*interfaces.KNPublishResult, error) {
	v.published = append(v.published, req)
	if v.publishErr != nil {
		return nil, v.publishErr
	}
	return &interfaces.KNPublishResult{KNVersion: &interfaces.KNVersion{Version: 3}}, nil
}

func (v *publishingVersions) ResetDraft(_ context.Context, _ string, req *interfaces.KNResetDraftRequest) (*interfaces.KNVersioningView, error) {
	v.reset = append(v.reset, req)
	return &interfaces.KNVersioningView{}, nil
}

// serveDirectWrite runs one PUT through a handler that writes the draft at revision 7 and
// replies with status.
func serveDirectWrite(t *testing.T, kvs *publishingVersions, query string, written bool, status int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &restHandler{kvs: kvs}
	engine := gin.New()
	engine.Use(r.finishDirectWrites())
	engine.PUT("/kn/:kn_id/object-types/:ot_id", func(c *gin.Context) {
		ctx := context.Background()
		if _, ok := r.writeBranch(&ctx, c, c.Param("kn_id")); !ok {
			return
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
	if got := recorder.Header().Get("X-Kn-Version"); got != "3" {
		t.Fatalf("X-Kn-Version %q", got)
	}
	if got := recorder.Header().Get("ETag"); got != `"r2"` {
		t.Fatalf("handler header lost: ETag %q", got)
	}
}

func TestDraftWriteIsNotPublished(t *testing.T) {
	kvs := &publishingVersions{}
	recorder := serveDirectWrite(t, kvs, "?branch=draft", true, http.StatusOK)
	// A write naming the draft still needs If-Match.
	if recorder.Code != http.StatusPreconditionRequired || len(kvs.published) != 0 {
		t.Fatalf("response %d, published %d", recorder.Code, len(kvs.published))
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

type enablingVersions struct {
	interfaces.KNVersionService
	err     error
	enabled []string
}

func (v *enablingVersions) EnableUnversioned(_ context.Context, knID string, notes string) (bool, error) {
	v.enabled = append(v.enabled, knID+":"+notes)
	return v.err == nil, v.err
}

func TestCreatedNetworkIsVersioned(t *testing.T) {
	kvs := &enablingVersions{}
	r := &restHandler{kvs: kvs}
	r.versionCreatedKN(context.Background(), "kn1", false)
	// An import that kept the existing network writes nothing new to version.
	r.versionCreatedKN(context.Background(), "kn2", true)
	if len(kvs.enabled) != 1 || kvs.enabled[0] != "kn1:"+createdKNNotes {
		t.Fatalf("enabled %v", kvs.enabled)
	}
	// A failure leaves the network to the server's next round instead of failing the create.
	kvs.err = errors.New("lock wait timeout")
	r.versionCreatedKN(context.Background(), "kn3", false)
}
