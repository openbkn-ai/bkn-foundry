// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// recordTestsTimeout bounds the reads of the data for one run of RecordTests: a regression waits
// for them before it starts.
const recordTestsTimeout = 30 * time.Second

// recordTestID is the id of the test RecordTests writes: no guidance test has it.
const recordTestID = "record"

// RecordTests writes, for each question named that the server can write a template test for, its
// template asked of a record in the data, as the caller: the slots filled with the values of one
// record (a document's example first), and checked as guidance checks its tests. A question asked
// without a passed test would otherwise be asked as its text, which names no record, and could
// only be answered by asking back which one. A question whose record the data does not bear out
// maps to nil; one the server writes no template test for is left out.
func (s *requirementService) RecordTests(ctx context.Context, caller interfaces.AccountInfo, projectID string,
	questionIDs []string) (map[string]*interfaces.ScenarioTest, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RecordTests")
	defer span.End()

	tests := map[string]*interfaces.ScenarioTest{}
	if len(questionIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return tests, nil
	}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !ok || req.Status != interfaces.REQUIREMENT_STATUS_READY {
		span.SetStatus(codes.Ok, "")
		return tests, nil
	}
	draft, ok, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !ok {
		// Without a draft no slot has a field: none can be filled.
		for _, id := range questionIDs {
			tests[id] = nil
		}
		span.SetStatus(codes.Ok, "")
		return tests, nil
	}
	unmapped := unmappedConcepts(&req.Spec)
	deadline := s.now() + recordTestsTimeout.Milliseconds()
	for _, q := range req.Spec.Questions {
		if q == nil || !slices.Contains(questionIDs, q.ID) {
			continue
		}
		written := serverTests(q, unmapped)
		if len(written) == 0 {
			continue
		}
		// Only the template asked of a record that exists: the question as it is meant.
		template := written[0]
		template.ID = recordTestID
		s.checkTests(ctx, caller, &req.Spec, draft, q, []*interfaces.QuestionTest{template}, deadline)
		if template.Check.Outcome != interfaces.TEST_CHECK_PASSED {
			tests[q.ID] = nil
			continue
		}
		tests[q.ID] = &interfaces.ScenarioTest{ID: template.ID, Text: template.Text, Expect: template.Expect}
	}
	span.SetStatus(codes.Ok, "")
	return tests, nil
}
