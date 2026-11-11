// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_KNVersionHTTPError(t *testing.T) {
	Convey("Test wording bkn-backend's version refusals as bkn-project's\n", t, func() {
		ctx := context.Background()
		backend := func(code string, details any) error {
			return &interfaces.BknBackendError{HTTPCode: http.StatusConflict,
				BaseError: httperrors.BaseError{ErrorCode: code, ErrorDetails: details}}
		}

		Convey("Each refusal has its status and code\n", func() {
			for _, c := range []struct {
				backend string
				status  int
				code    string
			}{
				{interfaces.KN_VERSION_ERROR_CONCEPT_INDEX_BUILDING, http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
				{interfaces.KN_VERSION_ERROR_CONCEPT_INDEX_FAILED, http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
				{interfaces.KN_VERSION_ERROR_PROXY_SYNC_REQUIRED, http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
				{"BknBackend.KnowledgeNetwork.Proxy.SyncPending", http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
				{interfaces.KN_VERSION_ERROR_IDEMPOTENCY_KEY_REUSED, http.StatusInternalServerError, perrors.BknProject_Release_InternalError},
				{interfaces.KN_VERSION_ERROR_RUNNING_MISMATCH, http.StatusConflict, perrors.BknProject_Release_RunningChanged},
				{interfaces.KN_VERSION_ERROR_PURGED, http.StatusGone, perrors.BknProject_Release_VersionPurged},
				{interfaces.KN_VERSION_ERROR_EDITION_RESTRICTED, http.StatusForbidden, perrors.BknProject_Rollback_EditionRestricted},
			} {
				err, ok := KNVersionHTTPError(ctx, backend(c.backend, nil))

				So(ok, ShouldBeTrue)
				var httpErr *httperrors.HTTPError
				So(errors.As(err, &httpErr), ShouldBeTrue)
				So(httpErr.HTTPCode, ShouldEqual, c.status)
				So(httpErr.BaseError.ErrorCode, ShouldEqual, c.code)
			}
		})

		Convey("The details bkn-backend gave are kept, also when wrapped\n", func() {
			details := map[string]any{"running_version": 3}
			err, ok := KNVersionHTTPError(ctx, fmt.Errorf("publish: %w", backend(interfaces.KN_VERSION_ERROR_RUNNING_MISMATCH, details)))

			So(ok, ShouldBeTrue)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.ErrorDetails, ShouldResemble, details)
		})

		Convey("Any other error is not a version refusal\n", func() {
			_, ok := KNVersionHTTPError(ctx, backend("BknBackend.Other", nil))
			So(ok, ShouldBeFalse)
			_, ok = KNVersionHTTPError(ctx, errors.New("timeout"))
			So(ok, ShouldBeFalse)
		})
	})
}
