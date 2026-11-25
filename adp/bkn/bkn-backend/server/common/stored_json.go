// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"bytes"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

// UnmarshalStoredJSON treats blank legacy columns as unset. It decodes populated
// columns with JSON v2 and preserves exact numbers in dynamic values.
func UnmarshalStoredJSON(raw []byte, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return jsoncodec.Unmarshal(raw, target)
}
