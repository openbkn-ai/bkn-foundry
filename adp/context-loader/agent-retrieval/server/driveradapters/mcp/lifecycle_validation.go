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
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
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
	if err := schema.Validate(arguments); err == nil {
		return nil
	}
	return invalidLifecycleArguments(lifecycleArgumentGuidance(name))
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

func lifecycleArgumentGuidance(name string) string {
	if name == "bkn_start_interaction" {
		return "bkn_start_interaction expects top-level agent_name, question, and conversation_mode; use continue with conversation_id or new without it"
	}
	return "bkn_finish_interaction expects top-level interaction_id and outcome, plus answer for completed or optional reason otherwise"
}
