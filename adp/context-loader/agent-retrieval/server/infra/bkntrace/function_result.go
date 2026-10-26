// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkntrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
)

const FunctionResultContract = "function_result_evidence/v1"
const FunctionResultSourceModule = "context-loader-function-result/v1"

// ResultProducer identifies a server-verified Function execution.
// A Function version is editable; version alone is not a code identity.
type ResultProducer struct {
	KnowledgeNetworkID string `json:"knowledge_network_id"`
	ToolboxID          string `json:"toolbox_id"`
	ToolID             string `json:"tool_id"`
	FunctionVersion    string `json:"function_version"`
	CodeSHA256         string `json:"code_sha256"`
}

func validResultCodeHash(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(value[7:])
	return err == nil && hex.EncodeToString(decoded) == value[7:]
}

// RecordFunctionResult captures the existing structured result from the platform execution.
// Core assigns the real assembly revision when materializing formal evidence.
func RecordFunctionResult(ctx context.Context, producer ResultProducer, listedVersion string, result map[string]any) error {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok || !EvidenceEnabled() {
		return errors.New("function evidence requires managed capture")
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > maxInlinePayloadBytes {
		return errors.New("function evidence requires an inline verifiable output")
	}
	var wire struct {
		StatusCode int `json:"status_code"`
		Execution  struct {
			ToolboxID       string `json:"toolbox_id"`
			ToolID          string `json:"tool_id"`
			FunctionVersion string `json:"function_version"`
			CodeSHA256      string `json:"code_sha256"`
		} `json:"function_execution"`
		Body struct {
			ExitCode json.RawMessage `json:"exit_code"`
			Result   json.RawMessage `json:"result"`
		} `json:"body"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.StatusCode != 200 ||
		wire.Execution.ToolboxID != producer.ToolboxID || wire.Execution.ToolID != producer.ToolID ||
		listedVersion == "" || listedVersion == "unversioned" || wire.Execution.FunctionVersion != listedVersion ||
		!validResultCodeHash(wire.Execution.CodeSHA256) {
		return errors.New("function producer execution identity is not verified")
	}
	producer.FunctionVersion, producer.CodeSHA256 = wire.Execution.FunctionVersion, wire.Execution.CodeSHA256
	if len(wire.Body.ExitCode) > 0 {
		var code *int
		if json.Unmarshal(wire.Body.ExitCode, &code) != nil || code == nil || *code != 0 {
			return errors.New("function execution did not succeed")
		}
	}
	// The assertion is only that this execution returned this result snapshot.
	// Do not interpret business fields or require a tool-owned evidence protocol.
	content := wire.Body.Result
	if len(content) == 0 || (content[0] != '{' && content[0] != '[') {
		return errors.New("function evidence requires an existing structured result")
	}
	ioCtx, release := TraceIOContext(ctx, "post")
	defer release()
	// Reserve the rest of the existing post budget for inner and outer Finish.
	deadline, _ := ioCtx.Deadline()
	ioCtx, cancel := context.WithTimeout(ioCtx, time.Until(deadline)/3)
	defer cancel()
	scope := payloadArtifactScope{InteractionID: ec.interactionID, OperationID: ec.operationID, Direction: "logic_result"}
	ref, hash, err := (evidencePayloadArtifactWriter{}).Put(withPayloadArtifactScope(ioCtx, scope), "application/json", content)
	if err != nil {
		// Diagnostic only: omit error/body/identity strings that may contain secrets.
		logger.DefaultLogger().WithContext(ctx).Warnf("BKN Trace Function snapshot failed: stage=artifact_put interaction_id=%s operation_id=%s", ec.interactionID, ec.operationID)
		return err
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	payload := map[string]any{
		"complete": true, "result_contract": FunctionResultContract, "producer": producer,
		"result_artifact_ref": ref, "content_hash": hash, "output_hash": hex.EncodeToString(digest[:]),
	}
	event := buildEvent(ec, "logic.execution.observed", "context.function_result", payload, "", "")
	err = SubmitEvents(ioCtx, nil, nil, []Event{event})
	if err != nil {
		logger.DefaultLogger().WithContext(ctx).Warnf("BKN Trace Function snapshot failed: stage=publish interaction_id=%s operation_id=%s", ec.interactionID, ec.operationID)
	}
	return err
}
