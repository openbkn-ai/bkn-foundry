// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knlogicpropertyresolver

import (
	"context"
	"encoding/json"
	"testing"
)

// Metric params come out of bodies decoded with UseNumber (see
// drivenadapters.precisionJSON), so a millisecond timestamp arrives as
// json.Number. Without a branch for it the value fell to the default case and
// the range check silently passed anything.
func TestValidateTimestampReadsJSONNumber(t *testing.T) {
	s := &knLogicPropertyResolverService{}
	ctx := context.Background()

	if err := s.validateTimestamp(ctx, json.Number("1735689600000"), "start", "p1"); err != nil {
		t.Errorf("in-range json.Number rejected: %v", err)
	}
	if err := s.validateTimestamp(ctx, json.Number("1"), "start", "p1"); err == nil {
		t.Error("out-of-range json.Number accepted")
	}
	if err := s.validateTimestamp(ctx, json.Number("1.5"), "start", "p1"); err == nil {
		t.Error("non-integer json.Number accepted")
	}
	if err := s.validateTimestamp(ctx, float64(1735689600000), "start", "p1"); err != nil {
		t.Errorf("in-range float64 rejected: %v", err)
	}
	if err := s.validateTimestamp(ctx, int64(1735689600000), "start", "p1"); err != nil {
		t.Errorf("in-range int64 rejected: %v", err)
	}
}
