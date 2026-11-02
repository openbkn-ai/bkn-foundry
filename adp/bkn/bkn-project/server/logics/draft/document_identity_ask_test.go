// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestModelAssistantSeesBusinessKeyChoicesWithoutHumanAuditProof(t *testing.T) {
	object := &interfaces.DraftObjectType{ID: "line", Document: &interfaces.DraftDocumentModel{Role: "in_document", SentenceIdentity: &interfaces.SentenceBusinessIdentity{Properties: []string{"number"}, ConfirmedBy: interfaces.AccountInfo{ID: "private-reviewer-id", Type: "user"}, ConfirmedAt: 123456789}}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}}}
	raw, err := json.Marshal(askObjectTypeOf(object))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "sentence_business_key") || !strings.Contains(string(raw), "number") || strings.Contains(string(raw), "private-reviewer-id") || strings.Contains(string(raw), "123456789") {
		t.Fatal("missing business context or leaked audit metadata", string(raw))
	}
}
