// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// BKNService defines the BKN import and export service interface.
//
//go:generate mockgen -source ../interfaces/bkn_service.go -destination ../interfaces/mock/mock_bkn_service.go
type BKNService interface {
	// ExportToTar exports a knowledge network as a tar archive, under the specification spec
	// selects (BKN_EXPORT_SPEC_*).
	ExportToTar(ctx context.Context, knID string, branch string, spec string) ([]byte, error)

	// CheckNetwork checks the definitions of a knowledge network branch, of any specification,
	// without changing anything.
	CheckNetwork(ctx context.Context, knID string, branch string) (*NetworkCheckResult, error)

	// DiffNetworks compares two knowledge network branches definition by definition.
	DiffNetworks(ctx context.Context, req KNDiffRequest) (*KNDiffResult, error)
}

// The specification an export is written in. Auto writes 3.0 when the network carries a declaration
// only 3.0 can hold and 2.x otherwise, so a network without one exports exactly as before; 2.0
// writes 2.x and leaves such declarations out; 3.0 writes 3.0.
const (
	BKN_EXPORT_SPEC_AUTO = ""
	BKN_EXPORT_SPEC_2    = "2.0"
	BKN_EXPORT_SPEC_3    = "3.0"
)

// NetworkCheckResult is what the network check finds. It reads the definitions only; it does not
// look at source data.
type NetworkCheckResult struct {
	// MisplacedParameters are parameters whose value source sits in Source, as the legacy parameter
	// table wrote it. Execution drops them; a 3.0 export is refused until they are fixed.
	MisplacedParameters []NetworkCheckParameter `json:"misplaced_parameters"`
}

// NetworkCheckParameter is one misplaced parameter: where it is, what is stored and the suggested
// rewrite. Conflict means it already has a value that the rewrite would replace.
type NetworkCheckParameter struct {
	ActionTypeID  string                     `json:"action_type_id,omitempty"`
	ObjectTypeID  string                     `json:"object_type_id,omitempty"`
	LogicProperty string                     `json:"logic_property,omitempty"`
	Parameter     string                     `json:"parameter"`
	Current       NetworkCheckParameterValue `json:"current"`
	Suggested     NetworkCheckParameterValue `json:"suggested"`
	Conflict      bool                       `json:"conflict"`
}

// NetworkCheckParameterValue is the part of a parameter the check reads and rewrites.
type NetworkCheckParameterValue struct {
	Source    string `json:"source"`
	ValueFrom string `json:"value_from"`
	Value     any    `json:"value"`
}
