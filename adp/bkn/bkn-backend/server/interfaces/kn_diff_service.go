// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	bknsdk "bkn-backend/bkn-specification/bkn"
)

// KNRef names one side of a comparison.
type KNRef struct {
	KNID   string `json:"kn_id"`
	Branch string `json:"branch,omitempty"`
}

// KNDiffRequest asks for the difference between two knowledge network branches.
type KNDiffRequest struct {
	Base   KNRef `json:"base"`
	Target KNRef `json:"target"`

	// FallbackByName pairs definitions by name once id matching is exhausted. It is off by
	// default because it is a guess: two definitions can carry the same name and mean different
	// things, and a wrong pair reads exactly like a real modification.
	FallbackByName bool `json:"fallback_by_name,omitempty"`
}

// KNDiffSide echoes what was compared, so a stored result still says what it was.
type KNDiffSide struct {
	KNID   string `json:"kn_id"`
	Branch string `json:"branch"`
	Name   string `json:"name"`
}

// KNDiffResult is the comparison plus the identity of both sides.
type KNDiffResult struct {
	Base   KNDiffSide `json:"base"`
	Target KNDiffSide `json:"target"`

	*bknsdk.NetworkDiff
}
