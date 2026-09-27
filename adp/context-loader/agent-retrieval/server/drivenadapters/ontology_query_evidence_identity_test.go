package drivenadapters

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"go.uber.org/mock/gomock"
)

func TestOntologyQueryForwardsOnlyVerifiedApplicationPrincipal(t *testing.T) {
	for _, test := range []struct {
		name, authMethod, headerAccountID, want string
	}{
		{name: "oauth", authMethod: "oauth", headerAccountID: "user-1", want: "openbkn-sdk"},
		{name: "api key", authMethod: "api_key", headerAccountID: "user-1", want: "openbkn-sdk"},
		{name: "unverified service header", authMethod: "service_header", headerAccountID: "user-1"},
		{name: "mismatched subject", authMethod: "oauth", headerAccountID: "another-user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
				AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
				AuthMethod: test.authMethod, TokenInfo: &interfaces.TokenInfo{ClientID: "openbkn-sdk"},
			})
			headers := withVerifiedApplicationPrincipal(ctx, map[string]string{"x-account-id": test.headerAccountID})
			if got := headers["X-BKN-Application-Principal-ID"]; got != test.want {
				t.Fatalf("application principal = %q, want %q", got, test.want)
			}
		})
	}
}

func TestObjectQueryUsesVerifiedApplicationPrincipal(t *testing.T) {
	controller := gomock.NewController(t)
	client, httpClient := newObjectQueryClient(t, controller)
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
		AuthMethod: "oauth", TokenInfo: &interfaces.TokenInfo{ClientID: "openbkn-sdk"},
	})
	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if got := headers["X-BKN-Application-Principal-ID"]; got != "openbkn-sdk" {
				t.Fatalf("forwarded application principal = %q", got)
			}
			return 200, []byte(`{"datas":[]}`), nil
		})
	if _, err := client.QueryObjectInstances(ctx, &interfaces.QueryObjectInstancesReq{
		KnID: "kn-1", OtID: "ot-1", Limit: 1,
	}); err != nil {
		t.Fatal(err)
	}
}
