// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"reflect"
	"testing"
)

func TestNewReceiptProjectionDocumentDerivesKnowledgeNetworkIDs(t *testing.T) {
	document := NewReceiptProjectionDocument(Receipt{BusinessRefs: []BusinessRef{
		{RefID: "object:kn-b:forecast"},
		{RefID: "kn:kn-a"},
		{RefID: "resource:inventory"},
		{RefID: "object:kn-b:item"},
	}})
	if !reflect.DeepEqual(document.KnowledgeNetworkIDs, []string{"kn-a", "kn-b"}) {
		t.Fatalf("knowledge_network_ids = %v", document.KnowledgeNetworkIDs)
	}
}
