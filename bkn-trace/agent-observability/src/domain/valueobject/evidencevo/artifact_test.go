// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import (
	"strings"
	"testing"
)

func TestArtifactFingerprintIgnoresServerDerivedRecordScope(t *testing.T) {
	artifact := validArtifact()
	artifact.BusinessRefs = []string{"object:kn-a:forecast"}
	normalized, validationErrors := NormalizeArtifact(artifact)
	if len(validationErrors) != 0 {
		t.Fatalf("normalize artifact: %+v", validationErrors)
	}
	normalized.EffectiveSubjectID = "user-a"
	normalized.ApplicationPrincipalID = "app-a"

	legacy := normalized
	legacy.EffectiveSubjectID = ""
	legacy.ApplicationPrincipalID = ""
	legacy.KnowledgeNetworkIDs = nil

	currentFingerprint, err := ArtifactFingerprint(normalized)
	if err != nil {
		t.Fatalf("fingerprint current artifact: %v", err)
	}
	legacyFingerprint, err := ArtifactFingerprint(legacy)
	if err != nil {
		t.Fatalf("fingerprint legacy artifact: %v", err)
	}
	if currentFingerprint != legacyFingerprint {
		t.Fatalf("server-derived record scope must not change artifact idempotency: current=%s legacy=%s", currentFingerprint, legacyFingerprint)
	}
}

func TestNormalizeArtifactAcceptsInlineQuestionAndComputesStableHash(t *testing.T) {
	artifact := validArtifact()
	artifact.Content = map[string]any{
		"text": "2024 年 7 月有多少张需求预测单？",
		"filters": map[string]any{
			"month": "2024-07",
		},
	}

	normalized, validationErrors := NormalizeArtifact(artifact)

	if len(validationErrors) != 0 {
		t.Fatalf("unexpected validation errors: %+v", validationErrors)
	}
	if !strings.HasPrefix(normalized.ContentHash, "sha256:") || len(normalized.ContentHash) != len("sha256:")+64 {
		t.Fatalf("expected computed sha256 content hash, got %q", normalized.ContentHash)
	}

	reordered := artifact
	reordered.Content = map[string]any{
		"filters": map[string]any{"month": "2024-07"},
		"text":    "2024 年 7 月有多少张需求预测单？",
	}
	second, secondErrors := NormalizeArtifact(reordered)
	if len(secondErrors) != 0 || second.ContentHash != normalized.ContentHash {
		t.Fatalf("canonical content hash must not depend on map order: first=%s second=%s errors=%+v", normalized.ContentHash, second.ContentHash, secondErrors)
	}
}

func TestNormalizeArtifactHashMatchesCrossLanguageJSONWithoutHTMLEscaping(t *testing.T) {
	artifact := validArtifact()
	artifact.Content = "flowchart LR\nPO[采购订单<br/>]"
	artifact.ContentHash = "sha256:d66060c35bbc3f5f22a0afc9131d6f6670ca44fcd2121b448ea3dbac65879917"

	normalized, validationErrors := NormalizeArtifact(artifact)

	if len(validationErrors) != 0 {
		t.Fatalf("cross-language canonical JSON hash must be accepted: %+v", validationErrors)
	}
	if normalized.ContentHash != artifact.ContentHash {
		t.Fatalf("unexpected canonical hash: %s", normalized.ContentHash)
	}
}

func TestNormalizeArtifactValidatesRequiredFieldsAndSupportedType(t *testing.T) {
	artifact := validArtifact()
	artifact.ArtifactID = ""
	artifact.ArtifactType = "model_chain_of_thought"
	artifact.RequestID = ""
	artifact.ContentType = ""
	artifact.ObservedAt = "not-a-time"

	_, validationErrors := NormalizeArtifact(artifact)

	for _, path := range []string{"artifact_id", "artifact_type", "bkn.request.id", "content_type", "observed_at"} {
		if !hasValidationPath(validationErrors, path) {
			t.Fatalf("expected validation error for %s, got %+v", path, validationErrors)
		}
	}
}

func TestNormalizeArtifactRequiresExactlyOneContentLocation(t *testing.T) {
	artifact := validArtifact()
	artifact.Content = nil

	_, missingErrors := NormalizeArtifact(artifact)
	if !hasValidationCode(missingErrors, "ARTIFACT_CONTENT_REQUIRED") {
		t.Fatalf("expected missing content location error, got %+v", missingErrors)
	}

	artifact.Content = map[string]any{"text": "answer"}
	artifact.SnapshotRef = "snapshot:answer-001"
	_, ambiguousErrors := NormalizeArtifact(artifact)
	if !hasValidationCode(ambiguousErrors, "ARTIFACT_CONTENT_AMBIGUOUS") {
		t.Fatalf("expected ambiguous content location error, got %+v", ambiguousErrors)
	}
}

func TestNormalizeArtifactRejectsSecretsRecursively(t *testing.T) {
	tests := []struct {
		name    string
		content any
	}{
		{name: "token", content: map[string]any{"nested": map[string]any{"access_token": "secret"}}},
		{name: "cookie", content: map[string]any{"Cookie": "session=secret"}},
		{name: "password", content: map[string]any{"password": "secret"}},
		{name: "private key", content: "-----BEGIN PRIVATE KEY-----\nsecret"},
		{name: "authorization", content: map[string]any{"Authorization": "Bearer secret"}},
		{name: "password in text", content: "password=secret-value"},
		{name: "token in text", content: "access_token: secret-value"},
		{name: "cookie in text", content: "Cookie=session-secret"},
		{name: "api key field", content: map[string]any{"api_key": "secret"}},
		{name: "client secret field", content: map[string]any{"client_secret": "secret"}},
		{name: "generic secret field", content: map[string]any{"secret": "secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := validArtifact()
			artifact.Content = tt.content

			_, validationErrors := NormalizeArtifact(artifact)

			if !hasValidationCode(validationErrors, "ARTIFACT_SECRET_FORBIDDEN") {
				t.Fatalf("expected secret rejection, got %+v", validationErrors)
			}
		})
	}
}

func TestNormalizeArtifactAllowsOrdinaryBusinessURLButRejectsCredentialURL(t *testing.T) {
	artifact := validArtifact()
	artifact.Content = map[string]any{
		"business_page":     "https://portal.example.com/orders/ORD-2026-001?view=detail",
		"secretary_name":    "张三",
		"input_token_count": 128,
	}
	if _, validationErrors := NormalizeArtifact(artifact); len(validationErrors) != 0 {
		t.Fatalf("ordinary business URL must remain observable: %+v", validationErrors)
	}

	for _, value := range []string{
		"https://api.example.com/orders?token=secret-value",
		"https://api.example.com/orders?api_key=secret-value",
		"https://client:password@example.com/orders",
	} {
		t.Run(value, func(t *testing.T) {
			credentialArtifact := validArtifact()
			credentialArtifact.Content = map[string]any{"business_page": value}
			_, validationErrors := NormalizeArtifact(credentialArtifact)
			if !hasValidationCode(validationErrors, "ARTIFACT_SECRET_FORBIDDEN") {
				t.Fatalf("credential-bearing URL must be rejected: %+v", validationErrors)
			}
		})
	}
}

func TestNormalizeArtifactRequiresOpaqueSnapshotReference(t *testing.T) {
	for _, value := range []string{"snapshot:answer-001", "artifact:answer-001"} {
		t.Run("allows "+value, func(t *testing.T) {
			artifact := validArtifact()
			artifact.Content = nil
			artifact.SnapshotRef = value
			artifact.ContentHash = "sha256:" + strings.Repeat("a", 64)
			if _, validationErrors := NormalizeArtifact(artifact); len(validationErrors) != 0 {
				t.Fatalf("opaque snapshot reference must be accepted: %+v", validationErrors)
			}
		})
	}

	for _, value := range []string{
		"https://portal.example.com/snapshots/answer-001",
		"http://localhost/snapshots/answer-001",
		"s3://private-bucket/answer-001",
		"answer-001",
	} {
		t.Run("rejects "+value, func(t *testing.T) {
			artifact := validArtifact()
			artifact.Content = nil
			artifact.SnapshotRef = value
			artifact.ContentHash = "sha256:" + strings.Repeat("a", 64)
			_, validationErrors := NormalizeArtifact(artifact)
			if !hasValidationPath(validationErrors, "snapshot_ref") {
				t.Fatalf("snapshot_ref must reject non-opaque references: %+v", validationErrors)
			}
		})
	}
}

func TestNormalizeArtifactRejectsBareObjectStorageURL(t *testing.T) {
	for _, value := range []string{
		"s3://private-bucket/evidence.json",
		"https://private-bucket.s3.amazonaws.com/evidence.json",
		"https://private-bucket.oss-cn-shanghai.aliyuncs.com/evidence.json",
	} {
		t.Run(value, func(t *testing.T) {
			artifact := validArtifact()
			artifact.Content = map[string]any{"source": value}

			_, validationErrors := NormalizeArtifact(artifact)

			if !hasValidationCode(validationErrors, "ARTIFACT_STORAGE_URL_FORBIDDEN") {
				t.Fatalf("expected object storage URL rejection, got %+v", validationErrors)
			}
		})
	}
}

func TestNormalizeArtifactScansReferenceAndIdentityMetadata(t *testing.T) {
	artifact := validArtifact()
	artifact.SourceRef = "https://private-bucket.s3.amazonaws.com/evidence.json"
	artifact.BusinessRefs = []string{"object:kn_demo:forecast", "oss://private-bucket/evidence.json"}
	artifact.AgentOrApp = "password=secret-value"

	_, validationErrors := NormalizeArtifact(artifact)

	if !hasValidationCode(validationErrors, "ARTIFACT_STORAGE_URL_FORBIDDEN") ||
		!hasValidationCode(validationErrors, "ARTIFACT_SECRET_FORBIDDEN") {
		t.Fatalf("all artifact metadata must be scanned: %+v", validationErrors)
	}
}

func TestNormalizeArtifactRejectsMismatchedContentHash(t *testing.T) {
	artifact := validArtifact()
	artifact.ContentHash = "sha256:" + strings.Repeat("0", 64)

	_, validationErrors := NormalizeArtifact(artifact)

	if !hasValidationCode(validationErrors, "ARTIFACT_CONTENT_HASH_MISMATCH") {
		t.Fatalf("expected content hash mismatch, got %+v", validationErrors)
	}
}

func TestNormalizeArtifactCanonicalizesTimestampsToUTC(t *testing.T) {
	artifact := validArtifact()
	artifact.ObservedAt = "2026-07-26T10:00:00.123456789+08:00"
	artifact.AsOf = "2026-07-25T21:00:00-05:00"

	normalized, validationErrors := NormalizeArtifact(artifact)

	if len(validationErrors) != 0 {
		t.Fatalf("equivalent RFC3339 timestamps must be accepted: %+v", validationErrors)
	}
	if normalized.ObservedAt != "2026-07-26T02:00:00.123456789Z" ||
		normalized.AsOf != "2026-07-26T02:00:00Z" {
		t.Fatalf("artifact timestamps must use canonical UTC RFC3339Nano: %+v", normalized)
	}
}

func TestNormalizeArtifactRejectsUnsafeArtifactIDs(t *testing.T) {
	testCases := []string{
		"artifact/with/slash",
		"artifact with space",
		"\tartifact",
		strings.Repeat("a", 129),
	}
	for _, artifactID := range testCases {
		t.Run(artifactID, func(t *testing.T) {
			artifact := validArtifact()
			artifact.ArtifactID = artifactID

			_, validationErrors := NormalizeArtifact(artifact)

			if !hasValidationCode(validationErrors, "ARTIFACT_ID_INVALID") {
				t.Fatalf("unsafe artifact_id must be rejected: id=%q errors=%+v", artifactID, validationErrors)
			}
		})
	}
}

func validArtifact() EvidenceArtifact {
	return EvidenceArtifact{
		ArtifactID:    "artifact_question_001",
		ArtifactType:  ArtifactTypeQuestion,
		RequestID:     "req_artifact_001",
		TraceID:       "4bf92f3577b34da6a3ce929d0e0e4736",
		InteractionID: "interaction_001",
		OperationID:   "operation_001",
		ClaimID:       "claim_001",
		SourceRef:     "interaction:interaction_001",
		BusinessRefs:  []string{"object:kn_supply:forecast"},
		ContentType:   "application/json",
		SchemaVersion: ArtifactContractVersion,
		ObservedAt:    "2026-07-26T08:00:00Z",
		AsOf:          "2024-07-31T23:59:59Z",
		SourceVersion: "main",
		Content:       map[string]any{"text": "question"},
		TenantID:      "tenant_demo",
		AccountID:     "acct_demo",
		AccountType:   "app",
		AgentOrApp:    "supply-chain-agent",
		Initiator:     "studio-user",
	}
}

func hasValidationCode(errors ValidationErrors, code string) bool {
	for _, item := range errors {
		if item.Code == code {
			return true
		}
	}
	return false
}

func hasValidationPath(errors ValidationErrors, path string) bool {
	for _, item := range errors {
		if item.Path == path {
			return true
		}
	}
	return false
}
