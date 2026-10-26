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
	"os"
	"slices"
	"strings"
)

const BOMIntersectionContract = "bom.structure_intersection/v1"
const BOMIntersectionSourceModule = "context-loader-bom-intersection/v1"

// BOMProducer pins an operator-approved, self-contained Function implementation.
// A Function version is editable; version alone is not a code identity.
type BOMProducer struct {
	KnowledgeNetworkID string `json:"knowledge_network_id"`
	ToolboxID          string `json:"toolbox_id"`
	ToolID             string `json:"tool_id"`
	FunctionVersion    string `json:"function_version"`
	CodeSHA256         string `json:"code_sha256"`
}

type BOMIntersection struct {
	Products          []string `json:"products"`
	IncludeSubstitute bool     `json:"include_substitute"`
	Depth             *int     `json:"depth"`
	SharedCodes       []string `json:"shared_codes"`
	SharedCount       int      `json:"shared_count"`
	Caliber           string   `json:"caliber"`
}

// ConfiguredBOMProducer never accepts identity or approval from tool arguments.
// Missing/invalid configuration leaves ordinary Function execution unchanged.
func ConfiguredBOMProducer(kn, box, tool string) (BOMProducer, bool) {
	var producers []BOMProducer
	if json.Unmarshal([]byte(os.Getenv("BKN_TRACE_BOM_SHARED_PRODUCERS")), &producers) != nil {
		return BOMProducer{}, false
	}
	for _, p := range producers {
		if p.KnowledgeNetworkID == kn && p.ToolboxID == box && p.ToolID == tool &&
			p.FunctionVersion != "" && p.FunctionVersion != "unversioned" && validBOMCodeHash(p.CodeSHA256) {
			return p, true
		}
	}
	return BOMProducer{}, false
}

func validBOMCodeHash(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(value[7:])
	return err == nil && hex.EncodeToString(decoded) == value[7:]
}

// RecordBOMIntersection records a bounded, typed producer result, not a Claim.
// Core assigns the real assembly revision when materializing formal evidence.
func RecordBOMIntersection(ctx context.Context, approved BOMProducer, listedVersion string, arguments, result map[string]any) error {
	ec, ok := contextFromRequest(ctx, nil)
	if !ok || !EvidenceEnabled() {
		return errors.New("BOM evidence requires managed capture")
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > maxInlinePayloadBytes {
		return errors.New("BOM evidence requires an inline verifiable output")
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
			ExitCode int             `json:"exit_code"`
			Result   BOMIntersection `json:"result"`
		} `json:"body"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.StatusCode != 200 || wire.Body.ExitCode != 0 ||
		wire.Execution.ToolboxID != approved.ToolboxID || wire.Execution.ToolID != approved.ToolID ||
		wire.Execution.FunctionVersion != approved.FunctionVersion || listedVersion != approved.FunctionVersion ||
		wire.Execution.CodeSHA256 != approved.CodeSHA256 {
		return errors.New("BOM producer execution identity is not verified")
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return errors.New("invalid BOM output")
	}
	body, _ := object["body"].(map[string]any)
	fields, _ := body["result"].(map[string]any)
	for _, key := range []string{"products", "include_substitute", "depth", "shared_codes", "shared_count", "caliber"} {
		if _, exists := fields[key]; !exists {
			return errors.New("incomplete BOM result")
		}
	}
	if _, ok := fields["shared_count"].(float64); !ok {
		return errors.New("BOM count must be an integer, including zero")
	}
	if _, ok := fields["include_substitute"].(bool); !ok {
		return errors.New("BOM substitute policy must be boolean")
	}
	if exit, exists := body["exit_code"]; exists {
		if code, ok := exit.(float64); !ok || code != 0 {
			return errors.New("BOM execution did not succeed")
		}
	}
	dataSource, _ := fields["data_source"].(map[string]any)
	datasets, _ := dataSource["loaded_datasets"].([]any)
	if dataSource["knowledge_network_id"] != approved.KnowledgeNetworkID || len(datasets) != 1 || datasets[0] != "bom" {
		return errors.New("BOM result source does not match the approved knowledge network")
	}
	if fields["truncated"] == true || fields["partial"] == true {
		return errors.New("partial BOM result")
	}
	conclusion := wire.Body.Result
	var requested struct {
		Products          []string `json:"products"`
		IncludeSubstitute bool     `json:"include_substitute"`
		Depth             *int     `json:"depth"`
	}
	input, err := json.Marshal(arguments)
	if err != nil || json.Unmarshal(input, &requested) != nil {
		return errors.New("invalid BOM arguments")
	}
	products := make([]string, 0, len(requested.Products))
	for _, product := range requested.Products {
		if product = strings.TrimSpace(product); product != "" && !slices.Contains(products, product) {
			products = append(products, product)
		}
	}
	if len(products) < 2 || !slices.Equal(products, conclusion.Products) ||
		requested.IncludeSubstitute != conclusion.IncludeSubstitute || !sameBOMDepth(requested.Depth, conclusion.Depth) ||
		conclusion.Caliber != "structure_intersect_only" || conclusion.SharedCodes == nil ||
		conclusion.SharedCount != len(conclusion.SharedCodes) || conclusion.SharedCount > 5000 ||
		!slices.IsSorted(conclusion.SharedCodes) {
		return errors.New("BOM result does not match the structure-intersection contract")
	}
	for i, code := range conclusion.SharedCodes {
		if code == "" || strings.TrimSpace(code) != code || i > 0 && code == conclusion.SharedCodes[i-1] {
			return errors.New("BOM intersection contains an invalid material code")
		}
	}
	// Keep the claim artifact small: original rows and full detail remain in the
	// existing Operation output. This result says nothing about stock or kitting.
	content, err := json.Marshal(conclusion)
	if err != nil {
		return err
	}
	ioCtx, release := TraceIOContext(ctx, "post")
	defer release()
	scope := payloadArtifactScope{InteractionID: ec.interactionID, OperationID: ec.operationID, Direction: "logic_result"}
	ref, hash, err := (evidencePayloadArtifactWriter{}).Put(withPayloadArtifactScope(ioCtx, scope), "application/json", content)
	if err != nil {
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
		"result_contract": BOMIntersectionContract, "producer": approved, "conclusion": conclusion,
		"result_artifact_ref": ref, "content_hash": hash, "output_hash": hex.EncodeToString(digest[:]),
	}
	event := buildEvent(ec, "logic.execution.observed", "context.bom_structure_intersection", payload, "", "")
	return SubmitEvents(ioCtx, nil, nil, []Event{event})
}

func sameBOMDepth(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
