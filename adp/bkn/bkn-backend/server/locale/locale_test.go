// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestLocaleMessagesAreComplete(t *testing.T) {
	Register()
	Register()
	if err := i18n.ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
}

func TestValueTypeDetailsRender(t *testing.T) {
	Register()
	tests := []struct {
		name string
		data map[string]any
		zh   string
		en   string
	}{
		{"ModuleType", nil, "值类型的模块类型必须为 'value_type'。", "Value type module type must be 'value_type'."},
		{"DuplicatedIDInFile", map[string]any{"valueTypeID": "vt-1"}, "值类型 ID 'vt-1' 在请求体中重复出现。", "Value type ID 'vt-1' appears more than once in the request body."},
		{"DuplicatedNameInFile", map[string]any{"valueTypeName": "Amount"}, "值类型名称 'Amount' 在请求体中重复出现。", "Value type name 'Amount' appears more than once in the request body."},
		{"ValueTypeIDAlreadyExists", map[string]any{"valueTypeID": "vt-1"}, "值类型 ID vt-1 已存在。", "Value type ID vt-1 already exists."},
		{"ValueTypeNameAlreadyExists", map[string]any{"valueTypeName": "Amount"}, "值类型名称 Amount 已存在。", "Value type name Amount already exists."},
	}
	for _, tt := range tests {
		for language, want := range map[i18n.Language]string{i18n.SimplifiedChinese: tt.zh, i18n.AmericanEnglish: tt.en} {
			t.Run(tt.name+"/"+string(language), func(t *testing.T) {
				got, err := i18n.Render(language, "BknBackend.ValueType.InvalidParameter.Detail."+tt.name, tt.data)
				if err != nil || got != want {
					t.Fatalf("Render() = %q, %v; want %q", got, err, want)
				}
			})
		}
	}
}

func TestAuditMessagesPreserveBusinessLanguageAndLiteralNames(t *testing.T) {
	Register()
	if got := AuditSummary("create", "knowledge_network", "{{ literal }}"); got != "新建领域知识网络“{{ literal }}”" {
		t.Fatalf("summary = %q", got)
	}
	if got := AuditNames([]string{"a", "b"}); got != "a、b，共2个" {
		t.Fatalf("names = %q", got)
	}
	if got := AuditNames([]string{"a", "b", "c", "d", "e", "f"}); got != "a、b、c、d、e等，共6个（名称摘要已截断）" {
		t.Fatalf("truncated names = %q", got)
	}
	if got := AuditNames(nil); got != "" {
		t.Fatalf("empty names = %q", got)
	}
}
