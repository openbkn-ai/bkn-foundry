// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"

	"github.com/bytedance/sonic"
)

// EncodeAliases is the f_aliases value of a definition: NULL without aliases, the way every row
// written before the column existed reads, otherwise a JSON array.
func EncodeAliases(aliases []string) (any, error) {
	if len(aliases) == 0 {
		return nil, nil
	}
	b, err := sonic.Marshal(aliases)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// DecodeStoredAliases reads f_aliases. NULL and an empty column are no aliases. A column that does
// not decode is read as no aliases too, and logged: aliases help find a definition, and one bad
// column must not make the definition, and the network read with it, unopenable.
func DecodeStoredAliases(ctx context.Context, raw []byte) []string {
	var aliases []string
	if err := UnmarshalStoredJSON(raw, &aliases); err != nil {
		LogSafeError(ctx, "Failed to unmarshal stored aliases", err)
		return nil
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}
