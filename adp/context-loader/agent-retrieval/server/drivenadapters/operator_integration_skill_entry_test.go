// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func skillEntryTestCtx() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
}

func newSkillEntryTestClient(t *testing.T) (*operatorIntegrationClient, *mocks.MockHTTPClient) {
	t.Helper()
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	return &operatorIntegrationClient{
		logger: logger, baseURL: "http://operator/api/agent-operator-integration", httpClient: httpClient,
	}, httpClient
}

// The execution factory refuses entry_point and entry_shell together, so each request form must
// put exactly its own keys on the wire.
func TestExecuteSkillSendsOneEntryForm(t *testing.T) {
	cases := []struct {
		name string
		req  *interfaces.ExecuteSkillRequest
		want map[string]any
	}{
		{
			name: "entry_point with arguments",
			req: &interfaces.ExecuteSkillRequest{SkillID: "skill-1", EntryPoint: "scripts/main.py",
				Arguments: []string{"--input", "a b.txt"}, Timeout: 30},
			want: map[string]any{"entry_point": "scripts/main.py", "arguments": []string{"--input", "a b.txt"}, "timeout": 30},
		},
		{
			name: "entry_point without arguments",
			req:  &interfaces.ExecuteSkillRequest{SkillID: "skill-1", EntryPoint: "main.py"},
			want: map[string]any{"entry_point": "main.py"},
		},
		{
			name: "untranslatable legacy entry_shell is forwarded",
			req:  &interfaces.ExecuteSkillRequest{SkillID: "skill-1", EntryShell: "bash run.sh"},
			want: map[string]any{"entry_shell": "bash run.sh"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, httpClient := newSkillEntryTestClient(t)
			var got map[string]any
			httpClient.EXPECT().Post(gomock.Any(), client.baseURL+"/internal-v1/caller/skills/skill-1/execute", gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body interface{}) (int, interface{}, error) {
					got = body.(map[string]any)
					return http.StatusOK, map[string]any{"skill_id": "skill-1", "exit_code": 0, "stdout": "ok"}, nil
				})
			resp, err := client.ExecuteSkill(skillEntryTestCtx(), tc.req)
			if err != nil || resp.Stdout != "ok" {
				t.Fatalf("ExecuteSkill() = (%+v, %v)", resp, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("request body = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// The runtime the execution factory reports reaches the caller unchanged.
func TestGetSkillContentPassesRuntimeThrough(t *testing.T) {
	client, httpClient := newSkillEntryTestClient(t)
	httpClient.EXPECT().Get(gomock.Any(), client.baseURL+"/internal-v1/caller/skills/skill-1/content", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ url.Values, _ map[string]string) (int, interface{}, error) {
			return http.StatusOK, map[string]any{
				"skill_id": "skill-1", "url": "http://asset/SKILL.md", "status": "published",
				"runtime": map[string]any{"runtime": "python", "executable": false, "reason": "shell only"},
			}, nil
		})
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://asset/SKILL.md", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte("# SKILL"), nil)

	content, err := client.GetSkillContent(skillEntryTestCtx(), "skill-1")
	if err != nil {
		t.Fatal(err)
	}
	want := &interfaces.SkillRuntime{Runtime: "python", Executable: false, Reason: "shell only"}
	if !reflect.DeepEqual(content.Runtime, want) {
		t.Fatalf("runtime = %+v, want %+v", content.Runtime, want)
	}
}
