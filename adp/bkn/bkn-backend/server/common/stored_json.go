// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"bytes"

	"github.com/bytedance/sonic"
)

// UnmarshalStoredJSON decodes a JSON column that is allowed to be empty.
//
// These columns are nullable in practice: rows written before a field existed, or by a path that
// left it blank, hold "" rather than "null" or "[]". Handing "" to the decoder fails with
// "the input json is empty", and the failure surfaces as an internal error on every read of the
// definition that row belongs to — and since a knowledge network is read with its definitions,
// the whole network becomes unopenable because one column is blank. An empty column means the
// value was never set, which is what an untouched target already says.
//
// Malformed content is still an error: only emptiness is read as "never set".
func UnmarshalStoredJSON(raw []byte, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return sonic.Unmarshal(raw, target)
}
