// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// DependencyError retains a dependency response for internal classification.
// Only Err is serialized at the response boundary; Body never enters the public envelope.
type DependencyError struct {
	Err  *httperrors.HTTPError `json:"-"`
	Body []byte                `json:"-"`
}

func (e *DependencyError) Error() string { return e.Err.Error() }
func (e *DependencyError) Unwrap() error { return e.Err }

func (e *DependencyError) MarshalJSON() ([]byte, error) { return json.Marshal(e.Err) }
