// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func linkAssessmentFixture() (*interfaces.Draft, []kb.DatasetWrite, kb.DatasetWrite) {
	draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "document", Document: &interfaces.DraftDocumentModel{Role: "evidence_document"}}}, RelationTypes: []*interfaces.DraftRelationType{
		{ID: "evidence", Source: "observation", Target: "document", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_reference"}},
		{ID: "association", Source: "observation", Target: "system", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}},
	}}}
	gold := []kb.DatasetWrite{{Spec: interfaces.DraftDatasetSpec{RelationTypeID: "evidence"}, Row: map[string]any{"document_id": "doc", "source_record_id": "source-a", "property_name": "number", "claim_value": "A"}}}
	actual := kb.DatasetWrite{Spec: interfaces.DraftDatasetSpec{RelationTypeID: "association"}, Row: map[string]any{"document_id": "doc", "source_record_id": "source-a", "target_record_id": "verified-target-a", "target_identity": map[string]any{"number": "A"}}}
	return draft, gold, actual
}
func TestLinkQualityUsesHumanMentionTruthRatherThanMatchingCoverage(t *testing.T) {
	for _, scenario := range []string{"correct", "wrong_target", "wrong_source", "other_file", "missing", "duplicate_evidence", "missing_prediction"} {
		t.Run(scenario, func(t *testing.T) {
			draft, gold, row := linkAssessmentFixture()
			actual := []kb.DatasetWrite{row}
			wantCorrect, wantExpected, wantPredicted := 1, 1, 1
			switch scenario {
			case "wrong_target":
				row.Row["target_identity"] = map[string]any{"number": "B"}
				wantCorrect = 0
			case "wrong_source":
				row.Row["source_record_id"] = "source-b"
				wantCorrect = 0
			case "other_file":
				row.Row["document_id"] = "another-file"
				wantCorrect = 0
			case "missing":
				actual = nil
				wantCorrect, wantPredicted = 0, 0
			case "duplicate_evidence":
				actual = append(actual, row)
			case "missing_prediction":
				gold = append(gold, kb.DatasetWrite{Spec: gold[0].Spec, Row: map[string]any{"document_id": "doc", "source_record_id": "source-b", "property_name": "number", "claim_value": "B"}})
				wantExpected = 2
			}
			previous := &AcceptanceCounts{Expected: 10, Predicted: 10, Correct: 10, Issues: map[string]int{}}
			score, err := assessLinkWrites(draft, gold, actual, previous)
			if err != nil || score.ExpectedLinks != wantExpected || score.PredictedLinks != wantPredicted || score.CorrectLinks != wantCorrect || score.Linking == nil || *score.Linking != float64(wantCorrect)/float64(wantExpected) {
				t.Fatal(score, err)
			}
			if score.Expected != 10 || score.Predicted != 10 || score.Correct != 10 || previous.ExpectedLinks != 0 || len(previous.Issues) != 0 {
				t.Fatal("link assessment rewrote extraction scores or prior receipt")
			}
			replay, err := assessLinkWrites(draft, gold, actual, score)
			if err != nil || replay.Issues["missing"] != score.Issues["missing"] || replay.Issues["unexpected_or_incorrect"] != score.Issues["unexpected_or_incorrect"] {
				t.Fatal("reassessment doubled issue counts", replay, err)
			}
		})
	}
}
func TestLinkAssessmentRejectsMissingTruthAndUnverifiedTargets(t *testing.T) {
	draft, gold, row := linkAssessmentFixture()
	if _, err := assessLinkWrites(draft, gold, []kb.DatasetWrite{row}, nil); err == nil {
		t.Fatal("missing extraction assessment accepted")
	}
	row.Row["target_record_id"] = ""
	if _, err := assessLinkWrites(draft, gold, []kb.DatasetWrite{row}, &AcceptanceCounts{Issues: map[string]int{}}); err == nil {
		t.Fatal("unverified target counted")
	}
}

func TestLinkQualityMeasuresConfirmedInDocumentAssociations(t *testing.T) {
	for _, target := range []string{"line-a", "line-b"} {
		draft := &interfaces.Draft{Items: interfaces.DraftItems{RelationTypes: []*interfaces.DraftRelationType{
			{ID: "contains", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "same_document"}},
			{ID: "evidence", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_reference"}},
		}}}
		spec := interfaces.DraftDatasetSpec{RelationTypeID: "contains"}
		expected := []kb.DatasetWrite{{Spec: spec, Row: map[string]any{"document_id": "doc", "source_record_id": "invoice", "target_record_id": "line-a"}}}
		actual := []kb.DatasetWrite{{Spec: spec, Row: map[string]any{"document_id": "doc", "source_record_id": "invoice", "target_record_id": target}}, {Spec: interfaces.DraftDatasetSpec{RelationTypeID: "evidence"}, Row: map[string]any{"document_id": "doc"}}}
		score, err := assessLinkWrites(draft, expected, actual, &AcceptanceCounts{Issues: map[string]int{}})
		if err != nil || score.ExpectedLinks != 1 || score.PredictedLinks != 1 || (score.CorrectLinks == 1) != (target == "line-a") {
			t.Fatal("in-document associations or evidence boundary scored incorrectly", score, err)
		}
	}
}

type linkAssessorStub struct {
	calls int
	err   error
}

func (s *linkAssessorStub) MeasureLinks(ctx context.Context, _ string, build *interfaces.KBBuild, _ *interfaces.Draft, rows []kb.DatasetWrite, guard func(context.Context) error) ([]*AcceptanceCounts, error) {
	s.calls++
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	if len(rows) == 0 || build.Files[0].Assessment == nil {
		return nil, ErrAcceptanceRequired
	}
	score := *build.Files[0].Assessment
	score.Issues = map[string]int{}
	score.ExpectedLinks, score.PredictedLinks, score.CorrectLinks = 1, 1, 1
	calculateAcceptance(&score)
	return []*AcceptanceCounts{&score}, nil
}
func TestAcceptanceLinkScoresCommitOnlyAfterAcknowledgedEdges(t *testing.T) {
	for _, scenario := range []string{"success", "missing_references", "reference_failure", "write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			service, queue, data, _, _, _ := systemLinkServiceFixture(t)
			build := queue.record.Build
			build.Mode = "acceptance"
			build.Files[0].Assessment = &AcceptanceCounts{Expected: 10, Predicted: 10, Correct: 10, Issues: map[string]int{}}
			build.Assessment = aggregateAcceptance(build.Files)
			assessor := &linkAssessorStub{}
			service.References = assessor
			if scenario == "missing_references" {
				service.References = nil
			}
			if scenario == "reference_failure" {
				assessor.err = ErrAcceptanceRequired
			}
			if scenario == "write_failure" {
				data.writeErr = errors.New("write outcome unknown")
			}
			preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", build.Owner)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", build.Owner, SystemLinkRequest{OperationID: uuid.NewString(), Signature: preview.Signature})
			if scenario == "success" {
				if err != nil || receipt.State != "succeeded" || queue.record.Build.Assessment == nil || !queue.record.Build.Assessment.Passed || queue.record.Build.Files[0].Assessment.CorrectLinks != 1 {
					t.Fatal("verified links never reached the independent gate", receipt, err)
				}
			} else {
				if err == nil || queue.record.Build.Files[0].Assessment.ExpectedLinks != 0 || queue.record.Build.Assessment.ExpectedLinks != 0 {
					t.Fatal("failed linking committed candidate scores", receipt, err)
				}
				if scenario != "write_failure" && len(data.written) != 0 {
					t.Fatal("missing independent references allowed writes")
				}
			}
		})
	}
}
func TestReferenceLinkMeasurementKeepsFrozenReviewAndExtractionCounts(t *testing.T) {
	service, store, data, build, draft, request := referenceServiceFixture(t)
	if _, err := service.Save(context.Background(), "Bearer operator-token", "project", build.Owner, request); err != nil {
		t.Fatal(err)
	}
	if err := service.Ready(context.Background(), build); err != nil {
		t.Fatal(err)
	}
	build.Files[0].State = "succeeded"
	build.Files[0].Assessment = &AcceptanceCounts{Expected: 10, Predicted: 10, Correct: 10, Issues: map[string]int{}}
	calls := 0
	guard := func(context.Context) error { calls++; return nil }
	scores, err := service.MeasureLinks(context.Background(), "Bearer operator-token", build, draft, nil, guard)
	if err != nil || len(scores) != 1 || scores[0].Expected != 10 || scores[0].Predicted != 10 || scores[0].Correct != 10 || scores[0].Passed || scores[0].Linking != nil || data.writes != 1 || calls == 0 {
		t.Fatal("unknown link denominator passed or references/extraction were changed", scores, err)
	}
	denied := errors.New("source permission revoked")
	if _, err := service.MeasureLinks(context.Background(), "Bearer operator-token", build, draft, nil, func(context.Context) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("revocation ignored", err)
	}
	store.record.Revision++
	if _, err := service.MeasureLinks(context.Background(), "Bearer operator-token", build, draft, nil, guard); !errors.Is(err, ErrAcceptanceRequired) {
		t.Fatal("changed gold revision used for old execution", err)
	}
}
