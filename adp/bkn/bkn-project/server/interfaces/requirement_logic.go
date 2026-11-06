// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"slices"
	"strconv"
	"strings"
)

// Kinetic needs are what the network must compute or do beyond its objects and relations: metrics,
// functions, Skills, MCP tools, APIs, applications and action types. The specification records what
// is needed, in business terms; the knowledge network holds how it is implemented.

// Kinds of kinetic needs.
const (
	LOGIC_NEED_METRIC      = "metric"
	LOGIC_NEED_FUNCTION    = "function"
	LOGIC_NEED_SKILL       = "skill"
	LOGIC_NEED_MCP_TOOL    = "mcp_tool"
	LOGIC_NEED_API         = "api"
	LOGIC_NEED_APP         = "app"
	LOGIC_NEED_ACTION_TYPE = "action_type"
)

// LogicNeedKinds are the kinds of kinetic needs, in the order they are listed.
var LogicNeedKinds = []string{LOGIC_NEED_METRIC, LOGIC_NEED_FUNCTION, LOGIC_NEED_SKILL, LOGIC_NEED_MCP_TOOL,
	LOGIC_NEED_API, LOGIC_NEED_APP, LOGIC_NEED_ACTION_TYPE}

// IsLogicNeedKind tells whether kind is a kind of kinetic need.
func IsLogicNeedKind(kind string) bool {
	return slices.Contains(LogicNeedKinds, kind)
}

// Metric types a metric need may suggest.
var LogicNeedMetricTypes = []string{"atomic", "derived", "composite"}

// Where a kinetic need comes from: the requirement documents as concepts were extracted, the
// analysis of the concept model, scenario drafting from an earlier specification, or a person.
const (
	LOGIC_NEED_FROM_DOCUMENTS = "documents"
	LOGIC_NEED_FROM_ANALYSIS  = "analysis"
	LOGIC_NEED_FROM_SCENARIOS = "scenarios"
	LOGIC_NEED_FROM_PERSON    = "person"
)

// REQUIREMENT_KIND_LOGIC_NEEDS is the route kind of kinetic needs.
const REQUIREMENT_KIND_LOGIC_NEEDS = "logic_needs"

// REQUIREMENT_CONCEPT_KINETIC is the category of a concept that is a kinetic need, such as a metric
// the documents define.
const REQUIREMENT_CONCEPT_KINETIC = "kinetic"

// RequirementLogicNeed is one kinetic need. Ids are n1, n2 and on, never given twice.
type RequirementLogicNeed struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Definition says what it computes or does, in business terms: for a metric its numerator,
	// denominator, filter and time attribution.
	Definition string `json:"definition"`
	// ObjectTypeIDs are the drafted object types it is about, the one it belongs to first.
	ObjectTypeIDs []string `json:"object_type_ids"`
	// Properties are the properties of those object types it reads.
	Properties []RequirementPropertyRef `json:"properties"`
	// SuggestedMetricType is atomic, derived or composite, for a metric only.
	SuggestedMetricType string `json:"suggested_metric_type,omitempty"`
	// ConceptIDs are the concepts it comes from.
	ConceptIDs []string `json:"concept_ids"`
	// ScenarioIDs are the scenarios it serves, as their questions rest on it.
	ScenarioIDs      []string              `json:"scenario_ids"`
	Citations        []RequirementCitation `json:"citations"`
	SupplementReason string                `json:"supplement_reason,omitempty"`
	Source           string                `json:"source"`
	Edited           bool                  `json:"edited"`
	Accepted         bool                  `json:"accepted"`
}

// RequirementPropertyRef is a property of a drafted object type, by its name.
type RequirementPropertyRef struct {
	ObjectTypeID string `json:"object_type_id"`
	Property     string `json:"property"`
}

// Kinds of requirement runs besides scenario drafting, which the specification's status tracks.
const (
	REQUIREMENT_RUN_CONCEPTS = "concepts"
	REQUIREMENT_RUN_KINETIC  = "kinetic"
)

// Statuses of a requirement run.
const (
	REQUIREMENT_RUN_RUNNING = "running"
	REQUIREMENT_RUN_READY   = "ready"
	REQUIREMENT_RUN_FAILED  = "failed"
)

// RequirementRun is the state of one run that writes a part of the specification: the concepts,
// rules, conflicts and choices extracted from the documents, or the kinetic needs analysed from the
// concept model. Only one run of a project goes at a time, scenario drafting included.
type RequirementRun struct {
	Status string `json:"status"`
	// Error is why it failed, as a code for the client to word.
	Error       string      `json:"error,omitempty"`
	RunID       string      `json:"run_id"`
	AgentTaskID string      `json:"agent_task_id,omitempty"`
	Retried     bool        `json:"retried,omitempty"`
	By          AccountInfo `json:"by"`
	StartTime   int64       `json:"start_time"`
	UpdateTime  int64       `json:"update_time"`
	// Inputs is what the last run of its kind that ended ready read, to tell when its part is out of
	// date; absent until one did. A run going or failed keeps the last one's.
	Inputs *RequirementInputs `json:"inputs,omitempty"`
	// Stale, computed when read, says its inputs changed since.
	Stale bool `json:"stale,omitempty"`
}

// Running tells whether the run is still going: running and started after staleBefore.
func (r *RequirementRun) Running(staleBefore int64) bool {
	return r != nil && r.Status == REQUIREMENT_RUN_RUNNING && r.StartTime >= staleBefore
}

// RunningRun is the kind of the run of the specification still going, empty when none is.
func (s *RequirementSpec) RunningRun(staleBefore int64) string {
	for kind, run := range s.Runs {
		if run.Running(staleBefore) {
			return kind
		}
	}
	return ""
}

// NormalizeLogicNeeds turns the capabilities and basis names of a specification drafted before
// kinetic needs into needs, the same ones however often it is read: capabilities of the scenarios
// first, then the metrics, functions and skills of the questions, merged by kind and name, numbered
// in that order. A question then rests on its needs by id. It does nothing once the specification
// has needs, or has nothing to turn.
func NormalizeLogicNeeds(spec *RequirementSpec) {
	if spec.LogicNeeds != nil {
		return
	}
	needs := []*RequirementLogicNeed{}
	need := func(kind, name, definition, scenarioID string) string {
		name = strings.TrimSpace(name)
		if name == "" {
			return ""
		}
		for _, n := range needs {
			if n.Kind == kind && strings.EqualFold(n.Name, name) {
				if n.Definition == "" {
					n.Definition = definition
				}
				if scenarioID != "" && !slices.Contains(n.ScenarioIDs, scenarioID) {
					n.ScenarioIDs = append(n.ScenarioIDs, scenarioID)
				}
				return n.ID
			}
		}
		n := &RequirementLogicNeed{
			ID: "n" + strconv.Itoa(len(needs)+1), Kind: kind, Name: name, Definition: definition,
			ObjectTypeIDs: []string{}, Properties: []RequirementPropertyRef{}, ConceptIDs: []string{},
			ScenarioIDs: []string{}, Citations: []RequirementCitation{}, Source: LOGIC_NEED_FROM_SCENARIOS,
		}
		if scenarioID != "" {
			n.ScenarioIDs = append(n.ScenarioIDs, scenarioID)
		}
		needs = append(needs, n)
		return n.ID
	}
	for _, s := range spec.Scenarios {
		for _, c := range s.Capabilities {
			kind := c.Kind
			if !IsLogicNeedKind(kind) {
				kind = LOGIC_NEED_APP
			}
			need(kind, c.Name, c.Definition, s.ID)
		}
		s.Capabilities = nil
	}
	for _, q := range spec.Questions {
		ids := q.LogicNeedIDs
		for _, group := range []struct {
			kind  string
			names []string
		}{{LOGIC_NEED_METRIC, q.Basis.Metrics}, {LOGIC_NEED_FUNCTION, q.Basis.Functions}, {LOGIC_NEED_SKILL, q.Basis.Skills}} {
			for _, name := range group.names {
				if id := need(group.kind, name, "", ""); id != "" && !slices.Contains(ids, id) {
					ids = append(ids, id)
					for _, n := range needs {
						if n.ID == id {
							for _, sid := range q.ScenarioIDs {
								if !slices.Contains(n.ScenarioIDs, sid) {
									n.ScenarioIDs = append(n.ScenarioIDs, sid)
								}
							}
						}
					}
				}
			}
		}
		if ids == nil {
			ids = []string{}
		}
		q.LogicNeedIDs = ids
		q.Basis.Metrics, q.Basis.Functions, q.Basis.Skills = nil, nil, nil
	}
	spec.LogicNeeds = needs
	if len(needs) > 0 {
		if spec.LastIDs == nil {
			spec.LastIDs = map[string]int{}
		}
		if spec.LastIDs["n"] < len(needs) {
			spec.LastIDs["n"] = len(needs)
		}
	}
}
