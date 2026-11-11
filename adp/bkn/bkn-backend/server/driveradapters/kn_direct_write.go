// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// A direct write is a write to a versioned network that does not name branch=draft: a script,
// the CLI or the SDK written before versioning. It lands in the draft like any other write and
// is then published in the same request, so the caller reads what it wrote as before. A draft
// that already holds someone else's unpublished changes is refused rather than published with
// it, and a direct write that fails, or whose publish fails, discards what it put in the draft.

const directWritesKey = "bkn_backend_direct_writes"

const directWriteDraftBusy = "the draft has unpublished changes; publish or discard them before " +
	"writing without branch=draft, or write with branch=draft"

type directWrite struct {
	ctx            context.Context
	knID           string
	dw             *logics.DraftWrite
	runningVersion int
}

type directWrites struct {
	original gin.ResponseWriter
	buffer   *bufferedResponse
	writes   []*directWrite
}

// isDirectWrite tells a direct write from one that names the draft.
func isDirectWrite(c *gin.Context, resolved *interfaces.KNResolvedBranch) bool {
	return resolved.Versioned && c.Query("branch") != interfaces.DRAFT_BRANCH
}

// startDirectWrite holds the response back until finishDirectWrites has published the draft.
func startDirectWrite(ctx context.Context, c *gin.Context, knID string, dw *logics.DraftWrite, runningVersion int) {
	dw.RefuseDraftChanges = true
	dw.RefuseDraftChangesDetail = directWriteDraftBusy
	write := &directWrite{ctx: ctx, knID: knID, dw: dw, runningVersion: runningVersion}
	if value, ok := c.Get(directWritesKey); ok {
		state := value.(*directWrites)
		for i, existing := range state.writes {
			if existing.knID == knID {
				state.writes[i] = write
				return
			}
		}
		state.writes = append(state.writes, write)
		return
	}
	buffer := newBufferedResponse(c.Writer)
	c.Set(directWritesKey, &directWrites{original: c.Writer, buffer: buffer, writes: []*directWrite{write}})
	c.Writer = buffer
}

// finishDirectWrites publishes what the direct writes of a request put in the draft, after the
// handler has run.
func (r *restHandler) finishDirectWrites() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		value, ok := c.Get(directWritesKey)
		if !ok {
			return
		}
		state := value.(*directWrites)
		c.Writer = state.original
		succeeded := state.buffer.Status() < http.StatusMultipleChoices
		for _, write := range state.writes {
			if !write.dw.Written {
				continue
			}
			if !succeeded {
				r.discardDirectWrite(write)
				continue
			}
			version, err := r.publishDirectWrite(c, write)
			if err != nil {
				r.discardDirectWrite(write)
				replyKNVersionError(c, err)
				return
			}
			if version > 0 {
				state.buffer.Header().Set("X-Kn-Version", strconv.Itoa(version))
				state.buffer.Header().Set("X-Kn-Running-Version", strconv.Itoa(version))
			}
			state.buffer.Header().Set("X-Kn-Draft-Revision", strconv.FormatInt(write.dw.Revision, 10))
		}
		state.buffer.flush()
	}
}

// publishDirectWrite publishes the draft a direct write produced and returns the new version,
// or 0 when the write changed nothing.
func (r *restHandler) publishDirectWrite(c *gin.Context, write *directWrite) (int, error) {
	running, revision := write.runningVersion, write.dw.Revision
	result, err := r.kvs.Publish(write.ctx, write.knID, &interfaces.KNPublishRequest{
		ExpectedRunningVersion: &running,
		ExpectedDraftRevision:  &revision,
		Notes:                  "Published by " + c.Request.Method + " " + c.FullPath(),
	})
	if err != nil {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) && httpErr.BaseError.ErrorCode == berrors.BknBackend_KNVersion_NothingToPublish {
			return 0, nil
		}
		return 0, err
	}
	return result.KNVersion.Version, nil
}

// discardDirectWrite puts the draft back to the running version, unless someone wrote to it
// after this request: the expected revision then no longer matches and their work stays.
func (r *restHandler) discardDirectWrite(write *directWrite) {
	revision := write.dw.Revision
	if _, err := r.kvs.ResetDraft(write.ctx, write.knID, &interfaces.KNResetDraftRequest{
		ExpectedDraftRevision: &revision,
		OverwriteChanges:      true,
	}); err != nil {
		otellog.LogError(write.ctx, "Discard the draft of a failed direct write failed", err)
	}
}

// bufferedResponse keeps a handler's response until it is flushed to the client.
type bufferedResponse struct {
	gin.ResponseWriter
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse(w gin.ResponseWriter) *bufferedResponse {
	return &bufferedResponse{ResponseWriter: w, header: http.Header{}}
}

func (w *bufferedResponse) Header() http.Header { return w.header }

func (w *bufferedResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *bufferedResponse) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *bufferedResponse) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	return w.body.Write(data)
}

func (w *bufferedResponse) WriteString(s string) (int, error) {
	w.WriteHeaderNow()
	return w.body.WriteString(s)
}

func (w *bufferedResponse) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *bufferedResponse) Size() int {
	if w.status == 0 {
		return -1
	}
	return w.body.Len()
}

func (w *bufferedResponse) Written() bool { return w.status != 0 }

func (w *bufferedResponse) Flush() {}

func (w *bufferedResponse) flush() {
	for name, values := range w.header {
		w.ResponseWriter.Header()[name] = values
	}
	if w.status == 0 {
		return
	}
	w.ResponseWriter.WriteHeader(w.status)
	if w.body.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
	}
}
