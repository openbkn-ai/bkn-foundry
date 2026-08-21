// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"

// unmarshalPrecise decodes a downstream body without rounding integers wider
// than float64. Every response carrying business data must go through it; see
// common.UnmarshalPreciseJSON for why, and PostBytes/GetBytes for how the raw body is
// obtained in the first place.
func unmarshalPrecise(data []byte, out any) error {
	return common.UnmarshalPreciseJSON(data, out)
}
