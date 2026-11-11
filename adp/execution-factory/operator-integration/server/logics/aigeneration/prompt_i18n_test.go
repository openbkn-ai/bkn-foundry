package aigeneration

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestLocalizedPromptTemplateUsesRequestLanguage(t *testing.T) {
	source := &interfaces.PromptTemplate{
		Name:         string(interfaces.PythonFunctionGenerator),
		SystemPrompt: "# 函数生成 Prompt 模板",
	}
	tests := []struct {
		language           i18n.Language
		description        string
		userPrompt         string
		systemPromptPrefix string
	}{
		{
			language:           i18n.SimplifiedChinese,
			description:        "Python函数生成Prompt模板",
			userPrompt:         "函数内容描述:%s; inputs:%v; outputs:%v;",
			systemPromptPrefix: "# 函数生成 Prompt 模板",
		},
		{
			language:           i18n.AmericanEnglish,
			description:        "Prompt template for Python function generation",
			userPrompt:         "Function description: %s; inputs: %v; outputs: %v;",
			systemPromptPrefix: "# Function Generation Prompt Template",
		},
	}

	for _, test := range tests {
		t.Run(string(test.language), func(t *testing.T) {
			ctx := i18n.WithLanguage(context.Background(), test.language)
			localized := localizedPromptTemplate(ctx, interfaces.PythonFunctionGenerator, source)
			if localized.Description != test.description || localized.UserPromptTemplate != test.userPrompt {
				t.Fatalf("localized prompt = %#v", localized)
			}
			if !strings.HasPrefix(localized.SystemPrompt, test.systemPromptPrefix) {
				t.Fatalf("system prompt starts with %q", localized.SystemPrompt)
			}
			if test.language == i18n.AmericanEnglish && containsHan(localized.SystemPrompt+localized.Description+localized.UserPromptTemplate) {
				t.Fatalf("English prompt contains Chinese text")
			}
			if source.Description != "" || source.UserPromptTemplate != "" || source.SystemPrompt != "# 函数生成 Prompt 模板" {
				t.Fatalf("source template was mutated: %#v", source)
			}
		})
	}
}

// run_sql and the two resource calls that feed it answer with raw physical
// columns, outside the property-level rules of the knowledge network. They are
// being removed from Context Loader, so the generator must not steer new
// functions to them; aggregation goes through the knowledge network instead.
func TestFunctionGeneratorPromptAvoidsPhysicalQueryCalls(t *testing.T) {
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		prompt, err := i18n.Render(language, "AgentOperatorIntegration.SystemPrompt.Python_Function_Generator.md", nil)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		for _, gone := range []string{"run_sql", "list_resources", "describe_resource"} {
			if strings.Contains(prompt, gone) {
				t.Errorf("%s prompt still mentions %s", language, gone)
			}
		}
		for _, want := range []string{"kn.query_metric(", "kn.run_cypher("} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s prompt does not mention %s", language, want)
			}
		}
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
