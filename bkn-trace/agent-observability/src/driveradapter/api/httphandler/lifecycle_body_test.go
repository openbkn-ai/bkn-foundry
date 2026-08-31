// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleBodyRejectsRemovedAndUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "removed business domain on business ref",
			body: `{"business_refs":[{"ref_type":"object_type","ref_id":"kn:kn-1:object_type:material",` +
				`"business_domain_id":"bd_public","version":"1"}]}`,
		},
		{
			name: "removed business domain on operation business edge",
			body: `{"operation_business_edges":[{"operation_id":"op-1","role":"read",` +
				`"observed_at":"2026-08-30T00:00:00Z","business_ref":{"ref_type":"object_type",` +
				`"ref_id":"kn:kn-1:object_type:material","business_domain_id":"bd_public","version":"1"}}]}`,
		},
		{
			name: "unknown business ref field",
			body: `{"business_refs":[{"ref_type":"object_type",` +
				`"ref_id":"kn:kn-1:object_type:material","not_a_field":"x","version":"1"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(test.body))
			response := httptest.NewRecorder()

			var decoded evidenceEventRequest
			if err := decodeLifecycleBody(response, request, &decoded); err == nil {
				t.Fatal("strict lifecycle contract accepted a removed or unknown field")
			}
		})
	}
}
