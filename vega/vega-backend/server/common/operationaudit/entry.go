// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import "time"

// Entry is one bounded Vega management attempt. The historical local table is
// retained as data, but this fact is no longer written or queried there.
type Entry struct {
	EventID        string
	EventTime      time.Time
	RecordedAt     time.Time
	ActorID        string
	ActorName      string
	ActorType      string
	AuthMethod     string
	RequestID      string
	SourceChannel  string
	Method         string
	HTTPStatus     int
	Action         string
	TargetType     string
	TargetID       string
	TargetName     string
	Outcome        string
	FailureCode    string
	FailureMessage string
	ChangedFields  []string
}
