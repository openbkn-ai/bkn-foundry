package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"
)

func TestExecuteCodeSyncSanitizesValidationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		status, want       int
	}{
		{"code limit", `{"detail":[{"type":"string_too_long","loc":["body","code"],"ctx":{"max_length":3},"input":"secret-token","msg":"http://internal"}]}`, "code_too_long", 422, 422},
		{"other field", `{"detail":[{"type":"string_too_long","loc":["body","token"],"ctx":{"max_length":3},"input":"secret-token"}]}`, "invalid_execution_input", 422, 422},
		{"malformed", `http://internal secret-token`, "invalid_execution_input", 422, 422},
		{"numeric location", `{"detail":[{"type":"string_too_long","loc":["body",0],"ctx":{"max_length":3},"input":"secret-token"}]}`, "invalid_execution_input", 422, 422},
		{"upstream unavailable", `{"input":"secret-token","detail":"http://internal"}`, "", 500, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			transport := mocks.NewMockHTTPClient(ctrl)
			transport.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(tc.status, []byte(tc.body), nil)
			client := &sandBoxControlPlaneClient{baseURL: "http://sandbox/api/v1", logger: logger.DefaultLogger(), executeHTTPClient: transport}
			_, err := client.ExecuteCodeSync(context.Background(), "s1", &interfaces.ExecuteCodeReq{Code: "你好世界"})
			he, ok := err.(*httperrors.HTTPError)
			if !ok || he.HTTPCode != tc.want {
				t.Fatalf("error=%v, want status %d", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "http://internal") {
				t.Fatalf("leaked response: %v", err)
			}
			if tc.reason != "" {
				details := he.ErrorDetails.(map[string]any)
				if details["reason"] != tc.reason || details["stage"] != "validation" || details["session_id"] != "s1" || he.ErrorCode != "Public.UnprocessableEntity" {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.reason == "code_too_long" && (details["actual"] != 4 || details["max"] != 3 || details["unit"] != "unicode_characters") {
					t.Fatalf("wrong character count: %v", details)
				}
			}
		})
	}
}

func TestExecuteCodeSyncCodeLimitBoundaries(t *testing.T) {
	for _, size := range []int{102399, 102400, 102401} {
		code := strings.Repeat("a", size)
		status := http.StatusOK
		body := []byte(`{"status":"completed","exit_code":0}`)
		if size > 102400 {
			status = 422
			body = []byte(`{"detail":[{"type":"string_too_long","loc":["body","code"],"ctx":{"max_length":102400}}]}`)
		}
		ctrl := gomock.NewController(t)
		transport := mocks.NewMockHTTPClient(ctrl)
		transport.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(status, body, nil)
		client := &sandBoxControlPlaneClient{baseURL: "http://sandbox/api/v1", logger: logger.DefaultLogger(), executeHTTPClient: transport}
		resp, err := client.ExecuteCodeSync(context.Background(), "s1", &interfaces.ExecuteCodeReq{Code: code})
		if status == 200 {
			if err != nil || resp.Status != "completed" {
				t.Fatalf("size %d: %v %v", size, resp, err)
			}
		} else {
			encoded, _ := json.Marshal(err.(*httperrors.HTTPError).ErrorDetails)
			if !strings.Contains(string(encoded), `"actual":102401`) {
				t.Fatalf("size %d: %v", size, err)
			}
		}
	}
}
