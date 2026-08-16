package aigeneration

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestLocalizedPromptTemplateUsesRequestLanguage(t *testing.T) {
	source := &interfaces.PromptTemplate{
		Name:         string(interfaces.PythonFunctionGenerator),
		SystemPrompt: "# 函数生成 Prompt 模板",
	}
	tests := []struct {
		language, description, userPrompt, systemPromptPrefix string
	}{
		{
			language:           sharedrest.SimplifiedChinese,
			description:        "Python函数生成Prompt模板",
			userPrompt:         "函数内容描述:%s; inputs:%v; outputs:%v;",
			systemPromptPrefix: "# 函数生成 Prompt 模板",
		},
		{
			language:           sharedrest.AmericanEnglish,
			description:        "Prompt template for Python function generation",
			userPrompt:         "Function description: %s; inputs: %v; outputs: %v;",
			systemPromptPrefix: "# Function Generation Prompt Template",
		},
	}

	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			ctx := sharedrest.WithLanguage(context.Background(), test.language)
			localized := localizedPromptTemplate(ctx, interfaces.PythonFunctionGenerator, source)
			if localized.Description != test.description || localized.UserPromptTemplate != test.userPrompt {
				t.Fatalf("localized prompt = %#v", localized)
			}
			if !strings.HasPrefix(localized.SystemPrompt, test.systemPromptPrefix) {
				t.Fatalf("system prompt starts with %q", localized.SystemPrompt)
			}
			if test.language == sharedrest.AmericanEnglish && containsHan(localized.SystemPrompt+localized.Description+localized.UserPromptTemplate) {
				t.Fatalf("English prompt contains Chinese text")
			}
			if source.Description != "" || source.UserPromptTemplate != "" || source.SystemPrompt != "# 函数生成 Prompt 模板" {
				t.Fatalf("source template was mutated: %#v", source)
			}
		})
	}
}

func containsHan(value string) bool {
	for _, char := range value {
		if char >= '\u4e00' && char <= '\u9fff' {
			return true
		}
	}
	return false
}
