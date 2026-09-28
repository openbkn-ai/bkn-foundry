// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import "errors"

// StoredConditionBuildError identifies a saved view condition that can no
// longer be compiled against the current source schema or index configuration.
type StoredConditionBuildError struct{ Cause error }

func (e *StoredConditionBuildError) Error() string {
	return "stored view condition cannot be built: " + e.Cause.Error()
}
func (e *StoredConditionBuildError) Unwrap() error { return e.Cause }

func AsStoredConditionBuildError(err error) (*StoredConditionBuildError, bool) {
	var target *StoredConditionBuildError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
