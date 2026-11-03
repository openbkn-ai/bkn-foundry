// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"testing"
)

func TestPublicationDoesNotReclassifyGeneralInfrastructureErrors(t *testing.T) {
	original := errors.New("git or draft database unavailable")
	if PublicError(context.Background(), original) != original {
		t.Fatal("non-KB infrastructure failure was relabeled as a KB failure")
	}
	for _, test := range []struct {
		err  error
		code string
	}{
		{&kb.SourceError{StatusCode: 403}, perrors.BknProject_Release_SourcePermission},
		{kb.ErrUnavailable, perrors.BknProject_Release_KnowledgeBaseUnavailable},
		{&kb.SourceError{StatusCode: 503}, perrors.BknProject_Release_KnowledgeBaseUnavailable},
		{ErrInvalidSnapshot, perrors.BknProject_Release_KnowledgeBaseNotReady},
	} {
		var converted *httperrors.HTTPError
		if !errors.As(PublicError(context.Background(), test.err), &converted) || converted.ErrorCode != test.code {
			t.Fatal("incorrect KB error classification")
		}
	}
}
