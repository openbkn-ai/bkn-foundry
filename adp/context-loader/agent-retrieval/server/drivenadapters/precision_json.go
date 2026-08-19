// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import "github.com/bytedance/sonic"

// precisionJSON is the decoder every downstream response carrying business data
// must go through.
//
// UseNumber keeps a JSON integer as json.Number — the literal digits — when it
// lands in an interface{} field. Without it the value becomes a float64, and
// float64 has 53 bits of mantissa: 9223372036854775808 (MySQL BIGINT past the
// signed maximum) rounds and re-serializes as 9.223372036854776e+18, and
// 18446744073709551615 (BIGINT UNSIGNED maximum) as 1.8446744073709552e+19.
//
// The interface{} fields are not incidental — object instances arrive as
// QueryObjectInstancesResp.Data []any, and their property values are whatever
// the customer's columns hold: BIGINT keys, ID card numbers, snowflake IDs.
// See openbkn-ai/bkn-studio#464.
//
// Encoding needs no counterpart: sonic and jsoniter both write json.Number back
// out as the original literal.
var precisionJSON = sonic.Config{UseNumber: true}.Froze()

// unmarshalPrecise decodes JSON without rounding integers wider than float64.
func unmarshalPrecise(data []byte, out any) error {
	return precisionJSON.Unmarshal(data, out)
}
