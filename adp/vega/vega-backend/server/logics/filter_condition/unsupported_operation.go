// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"errors"
	"fmt"
)

// Query channel. The same operator has different capabilities on different channels: The full-text and vector operators are only implemented on the index channel
// Table channel (SQL) does not exist and cannot exist.
const (
	QueryChannelSQL        = "sql"
	QueryChannelOpenSearch = "opensearch"
	QueryChannelFileset    = "fileset"
)

// UnsupportedOperationError said a filter operator is not implemented in the current query channel.
//
// This is a problem on the request side rather than a service failure: the caller can solve it by changing the operator or creating an index for the resource.
// The reason for using a named type instead of a naked error is that the error has to cross several layers before reaching the caller, with each layer in between
// Both need to determine whether "this is a parameter error" or "the downstream has really gone down" - the former is 400 and can self-correct, while the latter is 500.
// A bare fmt.Errorf loses its public error code at higher layers and is treated as an internal error.
type UnsupportedOperationError struct {
	Operation string // The operator name written in the request
	Channel   string // The actual query channel being executed
}

// NewUnsupportedOperationError structure does not support an operator error.
func NewUnsupportedOperationError(operation, channel string) *UnsupportedOperationError {
	return &UnsupportedOperationError{Operation: operation, Channel: channel}
}

func (e *UnsupportedOperationError) Error() string {
	msg := fmt.Sprintf("operation %s is not supported", e.Operation)
	if e.Channel != "" {
		msg += fmt.Sprintf(" by the %s query channel", e.Channel)
	}
	if hint := e.hint(); hint != "" {
		msg += "; " + hint
	}
	return msg
}

// hint provides an executable next step. The most common situation is to publish the full text/vector on table resources without local indexes
// Retrieval - Simply saying "not supported" will make the caller think that this capability does not exist, while in fact, creating an index will make it available.
func (e *UnsupportedOperationError) hint() string {
	if e.Channel != QueryChannelSQL {
		return ""
	}
	switch e.Operation {
	case OperationMatch, OperationMatchPhrase, OperationMultiMatch:
		return "full-text operations need a local index on the resource: build one, " +
			"or use like/not_like/contain/prefix/regex on this channel"
	case OperationKnnVector:
		return "vector search needs a local index with a vector feature on the field: build one first"
	}
	return ""
}

// Extracted UnsupportedOperationError AsUnsupportedOperationError chain from wrong.
func AsUnsupportedOperationError(err error) (*UnsupportedOperationError, bool) {
	var target *UnsupportedOperationError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// The IsFulltextOperation determines whether it is a full-text search operator. The full-text capability only exists in the index, so these
// Whether an operator can be used depends on whether the resource has a local index, rather than on the field type.
func IsFulltextOperation(operation string) bool {
	switch operation {
	case OperationMatch, OperationMatchPhrase, OperationMultiMatch:
		return true
	}
	return false
}
