// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"net/http"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/knversion"
)

// The version refusal codes are registered by comm-go/knversion. Registering them here
// as well stops a program that links this service with another one that answers with
// them from starting.
func TestVersionRefusalCodesAreNotRegisteredHere(t *testing.T) {
	for _, code := range knversion.RefusalCodes() {
		if slices.Contains(KNVersionErrCodeList, code) {
			t.Errorf("%s is registered by this service and by comm-go/knversion", code)
		}
		if err := httperrors.NewHTTPError(t.Context(), http.StatusConflict, code).Render(); err != nil {
			t.Errorf("render %s: %v", code, err)
		}
	}
}
