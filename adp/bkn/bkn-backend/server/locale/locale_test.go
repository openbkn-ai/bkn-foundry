// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"testing"
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
