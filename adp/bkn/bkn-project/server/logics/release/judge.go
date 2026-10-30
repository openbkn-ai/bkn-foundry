// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	// judgeAgentID is the bkn-agent task Agent that judges whether an answer answers its question.
	judgeAgentID = "bkn-project-answer-judge"
	// judgeDeadline bounds how long a judgement is waited for; judgePoll is how often it is read.
	judgeDeadline    = 2 * time.Minute
	judgePoll        = 2 * time.Second
	judgeReadTimeout = 10 * time.Second
)

// verdicts are what the judge may say, in its schema's order.
var verdicts = []string{
	interfaces.REGRESSION_VERDICT_ANSWERED,
	interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED,
	interfaces.REGRESSION_VERDICT_CANNOT_ANSWER,
	interfaces.REGRESSION_VERDICT_OFF_TOPIC,
}

// judgeNameProperty is the kind of a property's name; ObjectType says whose it is.
const judgeNameProperty = "property"

// judgeInput is the message the judge reads: the question asked, what a test question expects of
// the data, the answer as written, the tool calls behind it without what they returned, the needs
// by name, the display names of what the tool calls name, and the language of the reason.
type judgeInput struct {
	Question  string                   `json:"question"`
	Expect    string                   `json:"expect,omitempty"`
	Answer    string                   `json:"answer"`
	ToolCalls []judgeToolCall          `json:"tool_calls"`
	Needs     []interfaces.ReleaseItem `json:"needs"`
	Names     []judgeName              `json:"names,omitempty"`
	Language  string                   `json:"language"`
}

// judgeName is the display name of an object type, relation type or property, for the reason to
// name it by.
type judgeName struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	ObjectType string `json:"object_type,omitempty"`
}

type judgeToolCall struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

type judgeOutput struct {
	Verdict   string                          `json:"verdict"`
	Reason    string                          `json:"reason"`
	UsedNeeds []interfaces.RequirementItemRef `json:"used_needs"`
}

// judgeResponseFormat is the JSON Schema of a judgement.
func judgeResponseFormat() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdict": map[string]any{"type": "string", "enum": verdicts},
			"reason":  map[string]any{"type": "string"},
			"used_needs": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{"type": "string", "enum": []string{
						interfaces.RELEASE_ITEM_OBJECT_TYPE, interfaces.RELEASE_ITEM_RELATION_TYPE}},
					"id": map[string]any{"type": "string"},
				},
				"required":             []string{"kind", "id"},
				"additionalProperties": false,
			}},
		},
		"required":             []string{"verdict", "reason", "used_needs"},
		"additionalProperties": false,
	}
}

// judgeAnswer asks the judge, as the runner, whether answer answers question, and waits for it
// until the deadline. It returns the judgement, or the code of why there is none: the judge could
// not be run, failed or did not finish (judge_unavailable), or said what its schema does not allow
// or named a need it was not given (judge_invalid).
func (s *regressionService) judgeAnswer(ctx context.Context, runner interfaces.AccountInfo, question string,
	expect string, answer string, uses []interfaces.BknAgentToolUse, needs []interfaces.ReleaseItem, names []judgeName,
	language string) (*judgeOutput, string) {
	calls := make([]judgeToolCall, 0, len(uses))
	for _, u := range uses {
		calls = append(calls, judgeToolCall{Name: u.Name, Input: u.Input})
	}
	message, err := json.Marshal(judgeInput{Question: question, Expect: expect, Answer: answer, ToolCalls: calls, Needs: needs,
		Names: names, Language: language})
	if err != nil {
		return nil, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE
	}
	task, err := s.ba.RunTask(ctx, runner, &interfaces.BknAgentRunRequest{
		AgentID: judgeAgentID, Message: string(message), ResponseFormat: judgeResponseFormat(),
	})
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Run answer judge: %v", err))
		return nil, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE
	}
	deadline := s.now().Add(judgeDeadline)
	for !task.Terminal() {
		if !s.now().Before(deadline) {
			otellog.LogWarn(ctx, fmt.Sprintf("Answer judge task %s did not finish", task.TaskID))
			return nil, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE
		}
		s.sleep(judgePoll)
		// A failed read is tried again until the deadline: the task goes on in bkn-agent.
		readCtx, cancel := context.WithTimeout(ctx, judgeReadTimeout)
		read, err := s.ba.GetTask(readCtx, runner, task.TaskID)
		cancel()
		if err == nil {
			task = read
		}
	}
	if task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED {
		otellog.LogWarn(ctx, fmt.Sprintf("Answer judge task %s failed: %s", task.TaskID, task.FailureDetail))
		return nil, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE
	}
	var out judgeOutput
	if err := json.Unmarshal([]byte(task.Output), &out); err != nil || !slices.Contains(verdicts, out.Verdict) {
		return nil, interfaces.REGRESSION_CODE_JUDGE_INVALID
	}
	for _, used := range out.UsedNeeds {
		if !slices.ContainsFunc(needs, func(n interfaces.ReleaseItem) bool { return n.Kind == used.Kind && n.ID == used.ID }) {
			return nil, interfaces.REGRESSION_CODE_JUDGE_INVALID
		}
	}
	return &out, ""
}

// namesIn are the names in glossary of what the tool calls name: an id their input carries as a
// value or a key.
func namesIn(glossary []judgeName, uses []interfaces.BknAgentToolUse) []judgeName {
	said := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			said[v] = true
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for k, e := range v {
				said[k] = true
				walk(e)
			}
		}
	}
	for _, u := range uses {
		var input any
		if json.Unmarshal(u.Input, &input) == nil {
			walk(input)
		}
	}
	names := []judgeName{}
	for _, n := range glossary {
		if said[n.ID] {
			names = append(names, n)
		}
	}
	return names
}

// toolNamedIn is whether reason names a tool of uses.
func toolNamedIn(reason string, uses []interfaces.BknAgentToolUse) bool {
	return slices.ContainsFunc(uses, func(u interfaces.BknAgentToolUse) bool {
		return u.Name != "" && strings.Contains(reason, u.Name)
	})
}
