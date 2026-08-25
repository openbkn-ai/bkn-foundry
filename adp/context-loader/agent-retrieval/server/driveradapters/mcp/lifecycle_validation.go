// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mcp

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

var lifecycleArgumentSchemas = sync.OnceValue(func() map[string]*jsonschema.Schema {
	result := make(map[string]*jsonschema.Schema, len(lifecycleToolNames))
	for name := range lifecycleToolNames {
		input, _ := loadToolSchemas(name)
		var document any
		if err := json.Unmarshal(input, &document); err != nil {
			panic("invalid lifecycle input schema for " + name + ": " + err.Error())
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft7)
		if err := compiler.AddResource("schema.json", document); err != nil {
			panic("cannot register lifecycle input schema for " + name + ": " + err.Error())
		}
		schema, err := compiler.Compile("schema.json")
		if err != nil {
			panic("cannot compile lifecycle input schema for " + name + ": " + err.Error())
		}
		result[name] = schema
	}
	return result
})

func validateLifecycleArguments(name string, arguments map[string]any) *lifecycleError {
	if name == "bkn_finish_interaction" {
		if _, nested := arguments["bkn_context"]; nested {
			return invalidLifecycleArguments(
				"bkn_finish_interaction requires interaction_id as a top-level field; remove bkn_context and retry",
			)
		}
		if outcome, present := arguments["outcome"]; present && !validFinishOutcome(outcome) {
			return invalidLifecycleArguments(
				"bkn_finish_interaction outcome must be one of: " + strings.Join(finishInteractionOutcomes, ", "),
			)
		}
	}
	schema, ok := lifecycleArgumentSchemas()[name]
	if !ok {
		return nil
	}
	if err := schema.Validate(arguments); err != nil {
		if fields := unsupportedLifecycleArgumentFields(err); len(fields) > 0 {
			return invalidLifecycleArguments(
				name + " received unsupported field(s): " + strings.Join(fields, ", ") + ". Remove them and retry",
			)
		}
		return invalidLifecycleArguments(lifecycleArgumentGuidance(name, arguments))
	}
	if !validLifecycleFieldCombination(name, arguments) {
		return invalidLifecycleArguments(lifecycleArgumentGuidance(name, arguments))
	}
	return nil
}

func validLifecycleFieldCombination(name string, arguments map[string]any) bool {
	switch name {
	case "bkn_start_interaction":
		_, hasConversationID := arguments["conversation_id"]
		switch arguments["conversation_mode"] {
		case "continue":
			return hasConversationID
		case "new":
			return !hasConversationID
		}
	case "bkn_finish_interaction":
		if arguments["outcome"] == "completed" {
			answer, ok := arguments["answer"].(string)
			return ok && answer != ""
		}
	}
	return true
}

func unsupportedLifecycleArgumentFields(err error) []string {
	var root *jsonschema.ValidationError
	if !errors.As(err, &root) {
		return nil
	}
	fields := make(map[string]struct{})
	var visit func(*jsonschema.ValidationError)
	visit = func(validationErr *jsonschema.ValidationError) {
		if additional, ok := validationErr.ErrorKind.(*kind.AdditionalProperties); ok {
			for _, field := range additional.Properties {
				fields[field] = struct{}{}
			}
		}
		for _, cause := range validationErr.Causes {
			visit(cause)
		}
	}
	visit(root)
	result := make([]string, 0, len(fields))
	for field := range fields {
		result = append(result, field)
	}
	slices.Sort(result)
	return result
}

func invalidLifecycleArguments(message string) *lifecycleError {
	return &lifecycleError{
		Code:           "invalid_params",
		Message:        message,
		RequiredAction: "correct_tool_arguments",
	}
}

func validFinishOutcome(value any) bool {
	outcome, ok := value.(string)
	if !ok {
		return false
	}
	return slices.Contains(finishInteractionOutcomes, outcome)
}

func lifecycleArgumentGuidance(name string, arguments map[string]any) string {
	if name == "bkn_start_interaction" {
		switch arguments["conversation_mode"] {
		case "continue":
			return "bkn_start_interaction with conversation_mode=continue requires a non-empty conversation_id returned by the previous start call. Example: {\"conversation_mode\":\"continue\",\"question\":\"...\",\"agent_name\":\"...\",\"conversation_id\":\"conv_...\"}"
		case "new":
			if _, hasConversationID := arguments["conversation_id"]; hasConversationID {
				return "bkn_start_interaction with conversation_mode=new must omit conversation_id. Example: {\"conversation_mode\":\"new\",\"question\":\"...\",\"agent_name\":\"...\"}"
			}
		}
		return "bkn_start_interaction expects top-level agent_name, question, and conversation_mode; use continue with conversation_id or new without it"
	}
	if name == "bkn_finish_interaction" && arguments["outcome"] == "completed" {
		return "bkn_finish_interaction with outcome=completed requires a non-empty answer. Example: {\"interaction_id\":\"int_...\",\"outcome\":\"completed\",\"answer\":\"...\"}"
	}
	return "bkn_finish_interaction expects top-level interaction_id and outcome, plus answer for completed or optional reason otherwise"
}
