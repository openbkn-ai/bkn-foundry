// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package accesslog models the minimal user access facts emitted at the
// authentication boundary. It intentionally does not depend on HTTP or Hydra.
package accesslog

import (
	"context"
)

// Recorder is the fail-open boundary used by authentication handlers. The
// production implementation publishes to Kafka.
type Recorder interface {
	Record(context.Context, Entry) error
}

type Entry struct {
	ActorID           string
	ActorNameSnapshot string
	AuthMethod        string
	SourceChannel     string
	Action            string
	Outcome           string
	FailureCode       string
	RequestID         string
	ClientIP          string
}
