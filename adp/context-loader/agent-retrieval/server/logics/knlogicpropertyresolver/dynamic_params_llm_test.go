// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knlogicpropertyresolver

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/mocks"
)

// noopLogger 空实现，避免为每条日志写 gomock 期望
type noopLogger struct{}

func (l *noopLogger) Debug(...interface{})                                {}
func (l *noopLogger) Debugf(string, ...interface{})                       {}
func (l *noopLogger) Info(...interface{})                                 {}
func (l *noopLogger) Infof(string, ...interface{})                        {}
func (l *noopLogger) Warn(...interface{})                                 {}
func (l *noopLogger) Warnf(string, ...interface{})                        {}
func (l *noopLogger) Error(...interface{})                                {}
func (l *noopLogger) Errorf(string, ...interface{})                       {}
func (l *noopLogger) WithContext(context.Context) interfaces.Logger       { return l }
func (l *noopLogger) WithField(string, interface{}) interfaces.Logger     { return l }
func (l *noopLogger) WithFields(map[string]interface{}) interfaces.Logger { return l }

// TestChatJSON_SamplingParamsWithinGatewayRange 复现 issue #450：动态参数生成必须显式设置
// 采样参数。留空走 Go 零值时 mf-model-api 直接 400（要求 0 < top_p ≤ 1、top_k ≥ 1），
// 失败又被上层包装成「缺参」，根因被掩盖。
func TestChatJSON_SamplingParamsWithinGatewayRange(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mfClient := mocks.NewMockDrivenMFModelAPIClient(ctrl)

	var got *interfaces.LLMChatReq
	mfClient.EXPECT().Chat(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *interfaces.LLMChatReq) (string, error) {
			got = req
			return `{"ok": true}`, nil
		})

	d := newDynamicParamsLLM(&noopLogger{}, mfClient, nil)
	if _, err := d.chatJSON(context.Background(), "system", "{}", ""); err != nil {
		t.Fatalf("chatJSON failed: %v", err)
	}

	if got.TopP <= 0 || got.TopP > 1 {
		t.Errorf("TopP = %v, want 0 < top_p <= 1 (mf-model-api 校验区间)", got.TopP)
	}
	if got.TopK < 1 {
		t.Errorf("TopK = %v, want >= 1 (mf-model-api 校验区间)", got.TopK)
	}
	if got.MaxTokens < 10 {
		t.Errorf("MaxTokens = %v, want >= 10 (mf-model-api 校验区间)", got.MaxTokens)
	}
	if got.Temperature < 0 || got.Temperature > 2 {
		t.Errorf("Temperature = %v, want 0 <= temperature <= 2", got.Temperature)
	}
}
