// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func proxyAuthContext() interfaces.AccountAuthContext {
	return interfaces.AccountAuthContext{AccountID: "proxy-1", AccountType: interfaces.AccessorTypeApp}
}

func TestSkillReadsAsAnAccountUseTheCallerScopedRouteWithThatIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	client := &operatorIntegrationClient{
		logger: logger, baseURL: "http://operator/api/agent-operator-integration", httpClient: httpClient,
	}
	// A public caller: its own token must not travel on a read made as another account.
	ctx := common.SetRawTokenToCtx(common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	}), "caller-token")
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{RequestID: "req_12345678", OperationID: "op_parent"})
	assertHeaders := func(headers map[string]string) {
		t.Helper()
		if headers[string(interfaces.HeaderXAccountID)] != "proxy-1" ||
			headers[string(interfaces.HeaderXAccountType)] != string(interfaces.AccessorTypeApp) ||
			headers["Authorization"] != "" {
			t.Fatalf("explicit-account headers = %#v", headers)
		}
		// The read stays in the caller's trace.
		if headers[common.HeaderBKNRequestID] != "req_12345678" {
			t.Fatalf("trace headers = %#v", headers)
		}
		// The execution factory treats any trusted proxy-execution header as a proxy execution
		// context and refuses it on a read route; none may be sent.
		for key := range headers {
			if strings.HasPrefix(strings.ToLower(key), "x-bkn-") {
				t.Fatalf("proxy execution header %q sent on a read", key)
			}
		}
	}

	httpClient.EXPECT().Get(gomock.Any(), client.baseURL+"/internal-v1/caller/skills/skill-1/content", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ url.Values, headers map[string]string) (int, interface{}, error) {
			assertHeaders(headers)
			return http.StatusOK, map[string]any{"skill_id": "skill-1", "url": "http://asset/SKILL.md", "status": "published"}, nil
		})
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/SKILL.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("# SKILL"), nil)
	content, err := client.GetSkillContentAs(ctx, proxyAuthContext(), "skill-1")
	if err != nil || string(content.Content) != "# SKILL" || content.Status != "published" {
		t.Fatalf("GetSkillContentAs() = (%+v, %v)", content, err)
	}

	httpClient.EXPECT().Post(gomock.Any(), client.baseURL+"/internal-v1/caller/skills/skill-1/files/read", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, body interface{}) (int, interface{}, error) {
			assertHeaders(headers)
			if body.(map[string]string)["rel_path"] != "refs/a.md" {
				t.Fatalf("file read body = %v", body)
			}
			return http.StatusOK, map[string]any{"skill_id": "skill-1", "rel_path": "refs/a.md", "url": "http://asset/a.md"}, nil
		})
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/a.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("body"), nil)
	file, err := client.ReadSkillFileAs(ctx, proxyAuthContext(),
		&interfaces.ReadSkillFileRequest{SkillID: "skill-1", RelPath: "refs/a.md"})
	if err != nil || string(file.Content) != "body" {
		t.Fatalf("ReadSkillFileAs() = (%+v, %v)", file, err)
	}
}

func TestSkillReadAsAnIncompleteAccountFailsBeforeAnyRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	// No HTTP expectation: an incomplete account must not reach the execution factory.
	client := &operatorIntegrationClient{
		logger: mocks.NewMockLogger(ctrl), baseURL: "http://operator", httpClient: mocks.NewMockHTTPClient(ctrl),
	}
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	for name, account := range map[string]interfaces.AccountAuthContext{
		"no id":   {AccountType: interfaces.AccessorTypeApp},
		"no type": {AccountID: "proxy-1"},
	} {
		if _, err := client.GetSkillContentAs(ctx, account, "skill-1"); err == nil {
			t.Fatalf("%s: content read accepted", name)
		}
		if _, err := client.ReadSkillFileAs(ctx, account, &interfaces.ReadSkillFileRequest{SkillID: "skill-1", RelPath: "a.md"}); err == nil {
			t.Fatalf("%s: file read accepted", name)
		}
	}
}

func TestSkillReadAsAnAccountKeepsTheExecutionFactoryRefusal(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	client := &operatorIntegrationClient{logger: logger, baseURL: "http://operator", httpClient: httpClient}
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusForbidden, nil, errors.New("user has no permission"))

	_, err := client.GetSkillContentAs(ctx, proxyAuthContext(), "skill-1")
	if status, ok := infraErr.HTTPStatus(err); !ok || status != http.StatusForbidden {
		t.Fatalf("error = %v, want the 403 kept for the caller's message", err)
	}
}
