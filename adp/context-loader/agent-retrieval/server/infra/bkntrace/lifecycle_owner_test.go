package bkntrace

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"net/http"
	"testing"
)

func TestTrustedLifecycleOwnerMatchesHeaders(t *testing.T) {
	for _, tc := range []struct {
		name        string
		accountType interfaces.AccessorType
		client      string
		application string
		subjectType string
	}{
		{"OAuth user", interfaces.AccessorTypeUser, " sdk ", "sdk", "user"},
		{"service", interfaces.AccessorTypeApp, "", "account", "service"},
		{"account fallback", interfaces.AccessorTypeUser, "", "account", "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{AccountID: "account", AccountType: tc.accountType, TokenInfo: &interfaces.TokenInfo{ClientID: tc.client}})
			owner, err := TrustedLifecycleOwner(ctx)
			if err != nil {
				t.Fatal(err)
			}
			headers := http.Header{}
			if err := setTrustedLifecycleHeaders(ctx, headers); err != nil {
				t.Fatal(err)
			}
			if owner.ApplicationPrincipalID != tc.application || owner.EffectiveSubjectType != tc.subjectType || owner.EffectiveSubjectID != "account" || owner.DelegationID != "" {
				t.Fatalf("wrong authenticated owner: %#v", owner)
			}
			if headers.Get("X-BKN-Application-Principal-ID") != owner.ApplicationPrincipalID || headers.Get("X-BKN-Effective-Subject-Type") != owner.EffectiveSubjectType || headers.Get("X-BKN-Effective-Subject-ID") != owner.EffectiveSubjectID {
				t.Fatalf("owner/header mismatch: %#v", headers)
			}
		})
	}
	if _, err := TrustedLifecycleOwner(context.Background()); err == nil {
		t.Fatal("missing authenticated context accepted")
	}
}
