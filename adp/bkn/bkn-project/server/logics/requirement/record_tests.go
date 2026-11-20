// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
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
// only be answered by asking back which one. A question the data has no record for maps to nil;
// one the server writes no template test for, or whose data could not be read, is left out.
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
		// Without a draft no slot has a field: none can be filled, nor said to have no record.
		span.SetStatus(codes.Ok, "")
		return tests, nil
	}
	unmapped := unmappedConcepts(&req.Spec)
	deadline := s.now() + recordTestsTimeout.Milliseconds()
	// Reads of records the server or the edition makes stop with the run's budget, not their own.
	bounded, cancel := context.WithTimeout(ctx, recordTestsTimeout)
	defer cancel()
	for _, q := range req.Spec.Questions {
		if q == nil || !slices.Contains(questionIDs, q.ID) {
			continue
		}
		if s.now() >= deadline {
			// Out of time: the rest are asked as they are written.
			break
		}
		written := serverTests(q, unmapped)
		if len(written) == 0 {
			// No template: asked of a record of the first object type it needs.
			// Of the question's business needs: never of the files a knowledge base reads text from.
			asked := *q
			asked.Needs = businessNeeds(q, draft)
			if askable, filled := s.exampleTest(bounded, caller, draft, &asked); askable {
				tests[q.ID] = filled
			}
			continue
		}
		// Only the template asked of a record that exists: the question as it is meant.
		template := written[0]
		template.ID = recordTestID
		// Records a knowledge-base build wrote are read by the edition, not by the data's SQL.
		if document, filled, err := documentTemplate(bounded, caller, &req.Spec, draft, template); document {
			if err != nil {
				logger.Warnf("Read records for question %s failed: %v", q.ID, err)
				continue
			}
			tests[q.ID] = filled
			continue
		}
		s.checkTests(ctx, caller, &req.Spec, draft, q, []*interfaces.QuestionTest{template}, deadline)
		if template.Check.Outcome != interfaces.TEST_CHECK_PASSED {
			// Only data that has no such record says so; a read that failed, ran out of time or
			// could not be made says nothing of the data.
			if noRecord(template.Check) {
				tests[q.ID] = nil
			}
			continue
		}
		tests[q.ID] = &interfaces.ScenarioTest{ID: template.ID, Text: template.Text, Expect: template.Expect}
	}
	span.SetStatus(codes.Ok, "")
	return tests, nil
}

// noRecord tells whether a check of a template test found that the data has no record to fill it
// with: no row has the values, or the record named is not there.
func noRecord(check interfaces.QuestionTestCheck) bool {
	return check.Outcome == interfaces.TEST_CHECK_FAILED && check.Reason == checkRecordNotFound ||
		check.Outcome == interfaces.TEST_CHECK_NOT_CHECKED && check.Reason == checkNoValueInData
}
