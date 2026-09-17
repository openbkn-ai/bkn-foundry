// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

type PayloadMode string
type OperationProtocol string

const (
	PayloadInline     PayloadMode = "inline"
	PayloadReferenced PayloadMode = "referenced"
	PayloadOmitted    PayloadMode = "omitted"

	MaxInlinePayloadBytes         = 1 << 20
	PayloadOmittedReasonTooLarge  = "payload_too_large"
	PayloadOmittedReasonSerialize = "serialization_failed"
	PayloadOmittedReasonArtifact  = "artifact_write_failed"

	ProtocolMCP      OperationProtocol = "mcp"
	ProtocolSDK      OperationProtocol = "sdk"
	ProtocolInternal OperationProtocol = "internal"
)

func (protocol OperationProtocol) IsValid() bool {
	switch protocol {
	case ProtocolMCP, ProtocolSDK, ProtocolInternal:
		return true
	default:
		return false
	}
}

type PayloadEnvelope struct {
	Mode          PayloadMode     `json:"mode"`
	MediaType     string          `json:"media_type"`
	ByteLength    int             `json:"byte_length"`
	Inline        json.RawMessage `json:"inline,omitempty"`
	Ref           string          `json:"ref,omitempty"`
	OmittedReason string          `json:"omitted_reason,omitempty"`
}

func InlineJSONPayload(raw json.RawMessage) (PayloadEnvelope, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return PayloadEnvelope{}, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return PayloadEnvelope{}, err
	}
	if len(canonical) > MaxInlinePayloadBytes {
		return PayloadEnvelope{
			Mode: PayloadOmitted, MediaType: "application/json",
			ByteLength: len(canonical), OmittedReason: PayloadOmittedReasonTooLarge,
		}, nil
	}
	return PayloadEnvelope{
		Mode: PayloadInline, MediaType: "application/json",
		ByteLength: len(canonical), Inline: canonical,
	}, nil
}

func NormalizePayloadEnvelope(payload PayloadEnvelope) (PayloadEnvelope, error) {
	if payload.MediaType != "application/json" {
		return PayloadEnvelope{}, errors.New("payload media_type must be application/json")
	}
	switch payload.Mode {
	case PayloadInline:
		if len(payload.Inline) == 0 || payload.Ref != "" || payload.OmittedReason != "" {
			return PayloadEnvelope{}, errors.New("inline payload must contain only inline content")
		}
		normalized, err := InlineJSONPayload(payload.Inline)
		if err != nil || normalized.Mode != PayloadInline {
			return PayloadEnvelope{}, errors.New("inline payload must be valid JSON within the fixed limit")
		}
		return normalized, nil
	case PayloadReferenced:
		if payload.Inline != nil || payload.OmittedReason != "" ||
			payload.Ref != strings.TrimSpace(payload.Ref) ||
			!strings.HasPrefix(payload.Ref, "artifact:") ||
			len(strings.TrimPrefix(payload.Ref, "artifact:")) == 0 ||
			payload.ByteLength <= MaxInlinePayloadBytes {
			return PayloadEnvelope{}, errors.New("referenced payload requires a stable artifact ref above the inline limit")
		}
		return payload, nil
	case PayloadOmitted:
		if payload.Inline != nil || payload.Ref != "" {
			return PayloadEnvelope{}, errors.New("omitted payload cannot contain inline content or ref")
		}
		switch payload.OmittedReason {
		case PayloadOmittedReasonTooLarge:
			if payload.ByteLength <= MaxInlinePayloadBytes {
				return PayloadEnvelope{}, errors.New("payload_too_large requires a byte length above the inline limit")
			}
		case PayloadOmittedReasonSerialize, PayloadOmittedReasonArtifact:
			if payload.ByteLength < 0 {
				return PayloadEnvelope{}, errors.New("serialization_failed byte length cannot be negative")
			}
		default:
			return PayloadEnvelope{}, errors.New("omitted payload reason is invalid")
		}
		return payload, nil
	default:
		return PayloadEnvelope{}, errors.New("payload mode is invalid")
	}
}

type OperationCallFact struct {
	OperationID       string             `json:"operation_id"`
	Attempt           uint32             `json:"attempt"`
	ConversationID    string             `json:"conversation_id"`
	InteractionID     string             `json:"interaction_id"`
	ReceiptID         string             `json:"receipt_id,omitempty"`
	ToolName          string             `json:"tool_name"`
	Protocol          OperationProtocol  `json:"protocol"`
	SourceModule      string             `json:"source_module"`
	ParentOperationID string             `json:"parent_operation_id,omitempty"`
	CapabilityProfile *CapabilityProfile `json:"capability_profile,omitempty"`
	Input             PayloadEnvelope    `json:"input"`
	Output            *PayloadEnvelope   `json:"output,omitempty"`
	Error             *PayloadEnvelope   `json:"error,omitempty"`
	RequestID         string             `json:"request_id,omitempty"`
	TraceID           string             `json:"trace_id,omitempty"`
	SpanID            string             `json:"span_id,omitempty"`
	StartedAt         time.Time          `json:"started_at"`
	FinishedAt        *time.Time         `json:"finished_at,omitempty"`
	Status            AttemptStatus      `json:"status"`
	Retryable         bool               `json:"retryable"`
}

// CapabilityProfile freezes the internal producer contract selected for an
// operation attempt. It is Trace metadata and is not part of public MCP or SDK
// tool schemas.
type CapabilityProfile struct {
	ManifestID          string   `json:"manifest_id"`
	ManifestVersion     string   `json:"manifest_version"`
	CanonicalToolName   string   `json:"canonical_tool_name"`
	ToolVersion         string   `json:"tool_version"`
	InputSchemaDigest   string   `json:"input_schema_digest"`
	OutputSchemaDigest  string   `json:"output_schema_digest"`
	ExecutionRole       string   `json:"execution_role"`
	EvidenceContract    string   `json:"evidence_contract"`
	ChildEvidencePolicy string   `json:"child_evidence_policy"`
	MapperID            string   `json:"mapper_id,omitempty"`
	MapperVersion       string   `json:"mapper_version,omitempty"`
	MinimumTraceSchema  string   `json:"minimum_trace_schema,omitempty"`
	RequiredTraceFields []string `json:"required_trace_fields,omitempty"`
	FailurePolicy       string   `json:"failure_policy"`
	Resolution          string   `json:"resolution"`
	Reason              string   `json:"reason,omitempty"`
}

func NormalizeCapabilityProfile(profile *CapabilityProfile, toolName string) (*CapabilityProfile, error) {
	if profile == nil {
		return nil, nil
	}
	value := *profile
	value.RequiredTraceFields = append([]string(nil), profile.RequiredTraceFields...)
	sort.Strings(value.RequiredTraceFields)
	value.RequiredTraceFields = slices.Compact(value.RequiredTraceFields)
	required := []string{
		value.ManifestID, value.ManifestVersion, value.CanonicalToolName, value.ToolVersion,
		value.InputSchemaDigest, value.OutputSchemaDigest, value.ExecutionRole,
		value.EvidenceContract, value.ChildEvidencePolicy, value.FailurePolicy, value.Resolution,
	}
	for _, field := range required {
		if strings.TrimSpace(field) == "" {
			return nil, errors.New("capability profile has an empty required field")
		}
	}
	if value.CanonicalToolName != toolName {
		return nil, errors.New("capability profile tool identity does not match operation")
	}
	if value.Resolution != "matched" && value.Resolution != "execution_only" {
		return nil, errors.New("capability profile resolution is invalid")
	}
	if value.Resolution == "execution_only" && value.EvidenceContract != "execution_only" {
		return nil, errors.New("unmatched capability profile must be execution_only")
	}
	return &value, nil
}

func CapabilityProfilesEqual(left, right *CapabilityProfile) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.DeepEqual(left, right)
}

type OperationExecution struct {
	Fact              OperationCallFact `json:"fact"`
	Receipt           Receipt           `json:"receipt"`
	InteractionStatus InteractionStatus `json:"interaction_status"`
}
