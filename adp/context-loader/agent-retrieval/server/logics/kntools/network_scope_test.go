// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
)

func refusalDetail(t *testing.T, err error) (int, string) {
	t.Helper()
	httpErr, ok := err.(*httperrors.HTTPError)
	if !ok {
		t.Fatalf("error = %v, want an HTTP error", err)
	}
	detail, _ := httpErr.BaseError.ErrorDetails.(string)
	return httpErr.HTTPCode, detail
}

// A caller with only child grants, or a standalone grant on a tool or Skill, is refused by the
// network gate; the refusal has to say which grant is missing (#1550).
func TestCapabilityToolsNameTheNetworkGrantTheyNeed(t *testing.T) {
	ctx := context.Background()
	denied := infraErr.DefaultHTTPError(ctx, http.StatusForbidden, "generic")

	search := NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{}, &fakeKnAuthz{err: denied})
	_, err := search.SearchCapabilities(ctx, &SearchCapabilitiesReq{KnID: "kn-1"})
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.CapabilityNetworkViewRequired", map[string]any{})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	if status, detail := refusalDetail(t, err); status != http.StatusForbidden ||
		detail != localizedText {
		t.Fatalf("search refusal = %d %q", status, detail)
	}

	op := &fakeOperator{}
	execute := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/tool-1")}, &fakeKnAuthz{executeErr: denied})
	_, err = execute.ExecuteTool(ctx, &ExecuteToolReq{KnID: "kn-1", ToolboxID: "box-1", ToolID: "tool-1"})
	localizedText, renderErr = i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.CapabilityNetworkExecuteRequired", map[string]any{})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	if status, detail := refusalDetail(t, err); status != http.StatusForbidden ||
		detail != localizedText {
		t.Fatalf("execute refusal = %d %q", status, detail)
	}
	if op.executionCount != 0 {
		t.Fatal("a refused caller ran the tool")
	}
}

func TestCapabilityToolsKeepAnUnavailableGateAsIs(t *testing.T) {
	ctx := context.Background()
	unavailable := infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "down")

	_, err := NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{}, &fakeKnAuthz{err: unavailable}).
		SearchCapabilities(ctx, &SearchCapabilitiesReq{KnID: "kn-1"})
	if status, detail := refusalDetail(t, err); status != http.StatusServiceUnavailable || detail != "down" {
		t.Fatalf("unavailable gate = %d %q, want it unchanged", status, detail)
	}
}
