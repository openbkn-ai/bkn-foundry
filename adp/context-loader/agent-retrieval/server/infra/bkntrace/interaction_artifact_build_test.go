// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"reflect"
	"testing"
)

func TestBuildInteractionArtifactIsDeterministicWithoutPublishing(t *testing.T) {
	t.Setenv(envArtifactEndpoint, "")
	first, err := BuildInteractionArtifact(testTraceContext(), "conv", "int", InteractionArtifactResult, "answer")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildInteractionArtifact(testTraceContext(), "conv", "int", InteractionArtifactResult, "answer")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable artifact: first=%+v second=%+v err=%v", first, second, err)
	}
	if first["content"] != "answer" || first["artifact_type"] != "result" || first["interaction_id"] != "int" || first["application_principal_id"] == "" || first["content_hash"] == "" {
		t.Fatalf("missing governed metadata: %+v", first)
	}
	different, err := BuildInteractionArtifact(testTraceContext(), "conv", "int", InteractionArtifactResult, "different answer")
	if err != nil || different["artifact_id"] == first["artifact_id"] || different["content_hash"] == first["content_hash"] {
		t.Fatalf("answer identity failed: %+v %v", different, err)
	}
	if _, err = BuildInteractionArtifact(context.Background(), "conv", "int", InteractionArtifactResult, "answer"); err == nil {
		t.Fatal("untrusted context accepted")
	}
}
func TestDurableAnswerRequiresExplicitGate(t *testing.T) {
	for _, value := range []string{"", "false", "1", "invalid", "true"} {
		t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", value)
		if DurableAnswerEnabled() != (value == "true") {
			t.Fatalf("unexpected gate value %q", value)
		}
	}
}
