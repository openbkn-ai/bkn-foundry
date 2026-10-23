// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/stretchr/testify/require"
)

func TestLocaleMessages(t *testing.T) {
	Register()
	Register()
	require.NoError(t, i18n.ValidateMessageBundles())
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
