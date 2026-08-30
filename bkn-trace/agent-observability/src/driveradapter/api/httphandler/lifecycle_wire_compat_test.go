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

func TestLifecycleBodyIgnoresRetiredBusinessDomainOnBusinessRefs(t *testing.T) {
	body := `{"business_refs":[{"ref_type":"object_type","ref_id":"kn-1/object_type/material",` +
		`"business_domain_id":"bd_public","version":"1"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(body))
	response := httptest.NewRecorder()

	var decoded evidenceEventRequest
	if err := decodeLifecycleBody(response, request, &decoded); err != nil {
		t.Fatalf("pre-0.1.5 producer payload must stay acceptable: %v", err)
	}
	refs := businessRefsFromWire(decoded.BusinessRefs)
	if len(refs) != 1 || refs[0].RefID != "kn-1/object_type/material" || refs[0].Version != "1" {
		t.Fatalf("business ref did not survive the compatibility shim: %#v", refs)
	}
}

func TestLifecycleBodyStillRejectsUnknownFields(t *testing.T) {
	body := `{"business_refs":[{"ref_type":"object_type","ref_id":"kn-1/object_type/material","not_a_field":"x"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(body))
	response := httptest.NewRecorder()

	var decoded evidenceEventRequest
	if err := decodeLifecycleBody(response, request, &decoded); err == nil {
		t.Fatal("the strict lifecycle contract must still reject genuinely unknown fields")
	}
}
