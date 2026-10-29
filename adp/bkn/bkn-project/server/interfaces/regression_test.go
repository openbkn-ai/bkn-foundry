// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"testing"
)

// The JSON of a regression is what clients map to their copy: its field names and codes are a contract.
func TestRegressionJSON(t *testing.T) {
	r := Regression{
		Status: REGRESSION_STATUS_NEEDS_VALUES,
		Questions: []RegressionQuestion{{ID: "q1", Text: "T", Needs: []RequirementItemRef{},
			Tests: []RegressionTest{{ID: "t1", Text: "T1"}}}},
		Results: []RegressionResult{{
			QuestionID: "q1", TestID: "t1", Outcome: REGRESSION_OUTCOME_NOT_ANSWERED,
			Verdict: REGRESSION_VERDICT_CANNOT_ANSWER, Reason: "R",
			UsedNeeds: []RequirementItemRef{{Kind: "object_type", ID: "a"}},
		}},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "needs_values" {
		t.Fatalf("status = %v", got["status"])
	}
	question := got["questions"].([]any)[0].(map[string]any)
	if tests := question["tests"].([]any); tests[0].(map[string]any)["id"] != "t1" || tests[0].(map[string]any)["text"] != "T1" {
		t.Fatalf("tests = %v", tests)
	}
	result := got["results"].([]any)[0].(map[string]any)
	for key, want := range map[string]any{
		"question_id": "q1", "test_id": "t1", "outcome": "not_answered", "verdict": "cannot_answer", "reason": "R",
	} {
		if result[key] != want {
			t.Fatalf("%s = %v, want %v", key, result[key], want)
		}
	}
	if used := result["used_needs"].([]any)[0].(map[string]any); used["kind"] != "object_type" || used["id"] != "a" {
		t.Fatalf("used_needs = %v", used)
	}
	for code, want := range map[string]string{
		REGRESSION_OUTCOME_NEEDS_VALUES: "needs_values", REGRESSION_VERDICT_ANSWERED: "answered",
		REGRESSION_VERDICT_CLARIFICATION_NEEDED: "clarification_needed", REGRESSION_VERDICT_OFF_TOPIC: "off_topic",
		REGRESSION_CODE_JUDGE_UNAVAILABLE: "judge_unavailable", REGRESSION_CODE_JUDGE_INVALID: "judge_invalid",
	} {
		if code != want {
			t.Fatalf("code %q, want %q", code, want)
		}
	}
}

// A result of a template question has no test, and says nothing of one.
func TestRegressionResultWithoutTest(t *testing.T) {
	data, err := json.Marshal(RegressionResult{QuestionID: "q1", Outcome: REGRESSION_OUTCOME_PASSED})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	for _, key := range []string{"test_id", "verdict", "reason", "used_needs"} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s present in %s", key, data)
		}
	}
}
