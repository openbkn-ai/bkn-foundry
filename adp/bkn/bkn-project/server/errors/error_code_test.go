// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors_test

import (
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
)

func TestImportInitializesErrorCodes(t *testing.T) {
	codes := []string{
		perrors.BknProject_InvalidParameter_Limit,
		perrors.BknProject_InvalidParameter_Cursor,
	}
	codes = append(codes, perrors.ReleaseErrCodeList...)
	codes = append(codes, perrors.ProjectErrCodeList...)
	codes = append(codes, perrors.DraftErrCodeList...)
	for _, code := range append(codes, perrors.LineageErrCodeList...) {
		for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
			httpErr := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), language), 400, code)
			if err := httpErr.Render(); err != nil || httpErr.ErrorCode != code || httpErr.Description == "" {
				t.Fatalf("initialized error = %#v, %v", httpErr, err)
			}
		}
	}
}

// The codes a project's release replies with are registered, so each renders in both languages.
func TestProjectReleaseCodesAreRegistered(t *testing.T) {
	for _, code := range []string{
		perrors.BknProject_Release_InvalidRequestBody,
		perrors.BknProject_Release_InvalidAccepts,
		perrors.BknProject_Release_NotProjectRelease,
		perrors.BknProject_Release_NotOwner,
		perrors.BknProject_Release_ProjectArchived,
		perrors.BknProject_Release_ChecksFailed,
		perrors.BknProject_Release_ReviewOutdated,
	} {
		if !slices.Contains(perrors.ReleaseErrCodeList, code) {
			t.Fatalf("%s is not registered", code)
		}
	}
}
