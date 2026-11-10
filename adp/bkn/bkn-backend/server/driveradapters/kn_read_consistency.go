// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const versionedReadsKey = "kn_versioned_reads"

// markVersionedRead records a read of a version or of the draft, for consistentReads to check
// once the handler has finished.
func markVersionedRead(c *gin.Context, knID string, resolved *interfaces.KNResolvedBranch) {
	if !resolved.Versioned || resolved.Version == "" {
		return
	}
	reads, _ := c.Get(versionedReadsKey)
	list, _ := reads.([]versionedRead)
	c.Set(versionedReadsKey, append(list, versionedRead{knID: knID, resolved: resolved}))
}

type versionedRead struct {
	knID     string
	resolved *interfaces.KNResolvedBranch
}

// heldWriter keeps the response until the reads behind it are known to be whole.
type heldWriter struct {
	gin.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *heldWriter) WriteHeader(code int)              { w.status = code }
func (w *heldWriter) WriteHeaderNow()                   {}
func (w *heldWriter) Write(b []byte) (int, error)       { return w.body.Write(b) }
func (w *heldWriter) WriteString(s string) (int, error) { return w.body.WriteString(s) }
func (w *heldWriter) Status() int                       { return w.status }
func (w *heldWriter) Size() int                         { return w.body.Len() }
func (w *heldWriter) Written() bool                     { return w.body.Len() > 0 }
func (w *heldWriter) reset()                            { w.status = http.StatusOK; w.body.Reset() }
func (w *heldWriter) flush(real gin.ResponseWriter) error {
	real.WriteHeader(w.status)
	_, err := real.Write(w.body.Bytes())
	return err
}

// consistentReads checks, after the handler read a version or the draft, that it read one whole
// branch (design section 10): a purged version answers 410, and a draft written meanwhile runs
// the handler once more, then answers DraftRevisionMismatch.
func (r *restHandler) consistentReads() gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.kvs == nil || c.Param("kn_id") == "" {
			c.Next()
			return
		}
		real := c.Writer
		held := &heldWriter{ResponseWriter: real, status: http.StatusOK}
		c.Writer = held
		defer func() { c.Writer = real }()

		c.Next()
		for attempt := 0; ; attempt++ {
			err := r.checkReadsEnd(c)
			if err == nil {
				break
			}
			var httpErr *httperrors.HTTPError
			if attempt == 0 && errors.As(err, &httpErr) && httpErr.ErrorCode == berrors.BknBackend_KNVersion_DraftRevisionMismatch {
				c.Set(versionedReadsKey, []versionedRead(nil))
				held.reset()
				c.Handler()(c)
				continue
			}
			held.reset()
			replyKNVersionError(c, err)
			break
		}
		_ = held.flush(real)
	}
}

func (r *restHandler) checkReadsEnd(c *gin.Context) error {
	if c.Writer.Status() >= http.StatusBadRequest {
		return nil
	}
	reads, _ := c.Get(versionedReadsKey)
	list, _ := reads.([]versionedRead)
	for _, read := range list {
		if err := r.kvs.CheckReadEnd(i18n.GetLanguageCtx(c), read.knID, read.resolved); err != nil {
			return err
		}
	}
	return nil
}
