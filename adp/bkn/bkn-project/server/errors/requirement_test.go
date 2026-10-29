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

// The requirement codes are registered and worded in both languages, each with its own wording.
func TestRequirementCodesAreRegisteredAndWorded(t *testing.T) {
	codes := []string{
		perrors.BknProject_Requirement_InvalidRequestBody,
		perrors.BknProject_Requirement_MissingUpdateTime,
		perrors.BknProject_Requirement_UnsupportedFormat,
		perrors.BknProject_Requirement_NotText,
		perrors.BknProject_Requirement_DocumentTooLarge,
		perrors.BknProject_Requirement_TooManyDocuments,
		perrors.BknProject_Requirement_InvalidDocumentType,
		perrors.BknProject_Requirement_InvalidItem,
		perrors.BknProject_Requirement_InvalidResolution,
		perrors.BknProject_Requirement_InvalidOption,
		perrors.BknProject_Requirement_NoDocuments,
		perrors.BknProject_Requirement_NoCatalogs,
		perrors.BknProject_Requirement_InputsTooLarge,
		perrors.BknProject_Requirement_NotOwner,
		perrors.BknProject_Requirement_NotFound,
		perrors.BknProject_Requirement_DocumentNotFound,
		perrors.BknProject_Requirement_ItemNotFound,
		perrors.BknProject_Requirement_Drafting,
		perrors.BknProject_Requirement_Stale,
		perrors.BknProject_Requirement_HasDecisions,
		perrors.BknProject_Requirement_ProjectArchived,
		perrors.BknProject_Requirement_InternalError,
		perrors.BknProject_Requirement_InvalidTest,
		perrors.BknProject_Requirement_TestNotFound,
		perrors.BknProject_Requirement_Generating,
	}
	seen := map[i18n.Language]map[string]string{}
	for _, code := range codes {
		if !slices.Contains(perrors.ProjectErrCodeList, code) {
			t.Fatalf("%s is not in ProjectErrCodeList", code)
		}
		for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
			httpErr := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), language), 400, code)
			if err := httpErr.Render(); err != nil || httpErr.Description == "" {
				t.Fatalf("%s in %s = %#v, %v", code, language, httpErr, err)
			}
			if seen[language] == nil {
				seen[language] = map[string]string{}
			}
			if other, ok := seen[language][httpErr.Description]; ok {
				t.Fatalf("%s and %s share the wording %q in %s", code, other, httpErr.Description, language)
			}
			seen[language][httpErr.Description] = code
		}
	}
}
