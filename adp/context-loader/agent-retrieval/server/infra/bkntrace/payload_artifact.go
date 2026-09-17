// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

type payloadArtifactScope struct {
	InteractionID string
	OperationID   string
	Attempt       uint32
	Direction     string
}

type payloadArtifactScopeKey struct{}

func withPayloadArtifactScope(ctx context.Context, scope payloadArtifactScope) context.Context {
	return context.WithValue(ctx, payloadArtifactScopeKey{}, scope)
}

type evidencePayloadArtifactWriter struct{}

func (evidencePayloadArtifactWriter) Put(ctx context.Context, mediaType string, raw []byte) (string, string, error) {
	if mediaType != "application/json" || !json.Valid(raw) {
		return "", "", errors.New("payload artifact requires valid JSON")
	}
	ec, ok := baseEventContext(ctx)
	if !ok {
		return "", "", errors.New("payload artifact requires trusted trace context")
	}
	scope, _ := ctx.Value(payloadArtifactScopeKey{}).(payloadArtifactScope)
	if scope.InteractionID == "" {
		scope.InteractionID = ec.interactionID
	}
	if scope.OperationID == "" {
		scope.OperationID = ec.operationID
	}
	if scope.InteractionID == "" {
		return "", "", errors.New("payload artifact requires interaction identity")
	}
	var content any
	if err := common.UnmarshalPreciseJSON(raw, &content); err != nil {
		return "", "", err
	}
	digest, err := hashArtifactContent(content)
	if err != nil {
		return "", "", err
	}
	idSeed := strings.Join([]string{scope.InteractionID, scope.OperationID, scope.Direction, digest}, "|")
	idSum := sha256.Sum256([]byte(idSeed))
	artifactID := "art_payload_" + hex.EncodeToString(idSum[:16])
	artifactType := "data_result"
	if scope.Direction == "input" {
		artifactType = "query"
	}
	if scope.Direction == "error" {
		artifactType = "logic_execution"
	}
	artifact := map[string]any{
		"artifact_id": artifactID, "artifact_type": artifactType,
		"bkn.request.id": ec.requestID, "trace_id": ec.traceID,
		"interaction_id": scope.InteractionID, "operation_id": scope.OperationID,
		"content_type": mediaType, "schema_version": "2.2.0", "observed_at": ec.observedAt,
		"content_hash": digest, "content": content,
		"bkn.account.id": ec.accountID, "bkn.account.type": ec.accountType,
		"effective_subject_id": ec.accountID, "application_principal_id": ec.applicationID,
		"initiator": "account:" + ec.accountID, "agent_or_app": agentOrApp(ec),
	}
	if err := postArtifactWithRetry(evidenceArtifactURL(), evidenceTimeout(), traceBlockFromEventContext(ec), artifact); err != nil {
		return "", "", err
	}
	return "artifact:" + artifactID, digest, nil
}
