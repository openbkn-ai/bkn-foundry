// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"

	"github.com/bytedance/sonic"
)

// storedDeprecation is the f_deprecation column: why a definition is deprecated and what replaces it.
type storedDeprecation struct {
	Reason     string `json:"reason,omitempty"`
	ReplacedBy string `json:"replaced_by,omitempty"`
}

// EncodeLifecycle is the f_lifecycle_status and f_deprecation values of a lifecycle given as its
// status, reason and replaced_by. An active definition is NULL in both, the way every row written
// before the columns existed reads; f_deprecation holds reason and replaced_by only when deprecated.
func EncodeLifecycle(status, reason, replacedBy string) (any, any, error) {
	if status == "" || (status == "active" && reason == "" && replacedBy == "") {
		return nil, nil, nil
	}
	if reason == "" && replacedBy == "" {
		return status, nil, nil
	}
	b, err := sonic.Marshal(storedDeprecation{Reason: reason, ReplacedBy: replacedBy})
	if err != nil {
		return nil, nil, err
	}
	return status, string(b), nil
}

// DecodeStoredLifecycle reads f_lifecycle_status and f_deprecation into status, reason and
// replaced_by. NULL is active, returned as an empty status. An f_deprecation that does not decode is
// read as no reason, and logged: one bad column must not make the definition, and the network read
// with it, unopenable.
func DecodeStoredLifecycle(ctx context.Context, status, deprecation []byte) (string, string, string) {
	if len(status) == 0 {
		return "", "", ""
	}
	var d storedDeprecation
	if err := UnmarshalStoredJSON(deprecation, &d); err != nil {
		LogSafeError(ctx, "Failed to unmarshal stored deprecation", err)
		d = storedDeprecation{}
	}
	return string(status), d.Reason, d.ReplacedBy
}

// NullableString is the value of a nullable text column: NULL when empty.
func NullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
