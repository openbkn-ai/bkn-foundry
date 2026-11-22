// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A call made with an execution credential names the network it was issued for, so the Execution
// Factory can hold it there; one made with a sign-in names none.
func TestExecutionCredentialCallsNameTheirNetwork(t *testing.T) {
	client := &operatorIntegrationClient{baseURL: "http://operator/api/agent-operator-integration"}
	delegated := common.SetRawTokenToCtx(common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "delegator", AccountType: interfaces.AccessorTypeUser,
		TokenInfo: &interfaces.TokenInfo{VisitorID: "delegator", DelegationID: "run-1", DelegatedKnID: "kn-1"},
	}), "bex_run-1_secret")
	header, err := client.callerAuthorizationHeader(delegated, "operator.function.execute")
	if err != nil || header["Authorization"] != "Bearer bex_run-1_secret" || header["X-Kn-ID"] != "kn-1" {
		t.Fatalf("delegated header = %v, %v", header, err)
	}
	signedIn := common.SetRawTokenToCtx(common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{VisitorID: "user"},
	}), "ory_at_x")
	header, err = client.callerAuthorizationHeader(signedIn, "operator.function.execute")
	if err != nil || header["X-Kn-ID"] != "" {
		t.Fatalf("signed-in header = %v, %v", header, err)
	}
}
