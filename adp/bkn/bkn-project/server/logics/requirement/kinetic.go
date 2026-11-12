// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// The kinetic run analyses the confirmed concept model with the documents for the kinetic needs of
// the network: the metrics, functions, Skills, MCP tools, APIs, applications and action types its
// scenarios will rest on. Scenario drafting reads them after.

// agentLogicNeed is a kinetic need as the Agent writes it, its concepts by the ids it was given.
type agentLogicNeed struct {
	ID                  string             `json:"id"`
	Kind                string             `json:"kind"`
	Name                string             `json:"name"`
	Definition          string             `json:"definition"`
	ObjectTypeIDs       []string           `json:"object_type_ids"`
	Properties          []agentPropertyRef `json:"properties"`
	SuggestedMetricType string             `json:"suggested_metric_type"`
	ConceptIDs          []string           `json:"concept_ids"`
	Citations           []agentCitation    `json:"citations"`
	SupplementReason    string             `json:"supplement_reason"`
	// ExecutedBy is, for an action type, the id of the api or mcp_tool need that carries it out:
	// one of this output, or a kept- id.
	ExecutedBy string `json:"executed_by"`
}

type agentPropertyRef struct {
	ObjectTypeID string `json:"object_type_id"`
	Property     string `json:"property"`
}

// logicNeedSchema is the schema of a kinetic need.
func logicNeedSchema() map[string]any {
	return schemaObject(map[string]any{
		"id": schemaString(), "kind": schemaEnum(interfaces.LogicNeedKinds...), "name": schemaString(),
		"definition": schemaString(), "object_type_ids": schemaStrings(),
		"properties": schemaArray(schemaObject(map[string]any{
			"object_type_id": schemaString(), "property": schemaString(),
		}, "object_type_id", "property")),
		"suggested_metric_type": schemaEnum(append([]string{""}, interfaces.LogicNeedMetricTypes...)...),
		"concept_ids":           schemaStrings(), "citations": citationsSchema(), "supplement_reason": schemaString(),
		"executed_by": schemaString(),
	}, "id", "kind", "name", "definition", "citations")
}

// kineticOutput is what the Agent returns in pass kinetic.
type kineticOutput struct {
	LogicNeeds []agentLogicNeed `json:"logic_needs"`
}

func kineticResponseFormat() map[string]any {
	return schemaObject(map[string]any{"logic_needs": schemaArray(logicNeedSchema())}, "logic_needs")
}

// kineticMessage is the user message of pass kinetic: the documents block by block, the adopted
// items with their properties, the concepts and rules of the specification, and the kinetic needs
// it keeps, not to be repeated.
type kineticMessage struct {
	Documents     []messageDocument     `json:"documents"`
	ObjectTypes   []messageObjectType   `json:"object_types"`
	RelationTypes []messageRelationType `json:"relation_types"`
	Concepts      []messageConcept      `json:"concepts"`
	Rules         []messageRule         `json:"rules"`
	Kept          messageKeptNeeds      `json:"kept"`
}

type messageKeptNeeds struct {
	LogicNeeds []messageLogicNeed `json:"logic_needs"`
}

// The network's items go as kept needs too, after the specification's, so the Agent gives only
// what they leave uncovered.
func buildKineticMessage(language string, docs []*interfaces.RequirementDocument, draft *interfaces.Draft,
	spec *interfaces.RequirementSpec, items []*interfaces.LogicNetworkItem) (string, error) {
	msg := kineticMessage{Documents: messageDocumentsOf(docs), Concepts: messageConceptsOf(spec.Concepts),
		Rules: messageRulesOf(spec.Rules), Kept: messageKeptNeeds{LogicNeeds: []messageLogicNeed{}}}
	msg.ObjectTypes, msg.RelationTypes, _ = messageOntologyOf(draft, true)
	for _, n := range messageLogicNeedsOf(spec.LogicNeeds) {
		n.ID = keptPrefix + n.ID
		msg.Kept.LogicNeeds = append(msg.Kept.LogicNeeds, n)
	}
	msg.Kept.LogicNeeds = append(msg.Kept.LogicNeeds, messageNetworkItemsOf(items)...)
	body, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return "pass kinetic\nlanguage: " + language + "\n" + string(body), nil
}

// keepForKinetic drops from spec the kinetic needs the kinetic run analyses anew: those an earlier
// analysis or scenario drafting gave that no one made theirs and no question rests on. With discard
// set, only those questions rest on stay of them. Needs the documents state, or a person added, stay.
func keepForKinetic(spec *interfaces.RequirementSpec, discard bool) {
	// No id once given is given again, of a need dropped here too.
	withLists(spec)
	recordAllIDs(spec, spec)
	_, needs := questionRefs(spec.Questions)
	spec.LogicNeeds = slices.DeleteFunc(spec.LogicNeeds, func(n *interfaces.RequirementLogicNeed) bool {
		analysed := n.Source == interfaces.LOGIC_NEED_FROM_ANALYSIS || n.Source == interfaces.LOGIC_NEED_FROM_SCENARIOS
		return analysed && !needs[n.ID] && n.Link == nil && (discard || !n.Accepted && !n.Edited)
	})
}

// addLogicNeeds checks the kinetic needs the Agent proposed and adds those that pass to spec, with
// the server's ids, from source; concepts maps the ids the Agent named concepts by to theirs. A need
// repeating one of the same kind and name, or naming its kept- id, is merged into it: it adds its
// concepts, and its object types and properties when the need has none and no one changed it. A
// need stated in the documents quotes
// them; one the analysis adds may say why instead. Numbers in a definition must be in its quotes, a
// Skill must be one the documents name, and object types and properties must be adopted ones.
func addLogicNeeds(spec *interfaces.RequirementSpec, proposed []agentLogicNeed, source string,
	docs map[string]*interfaces.RequirementDocument, draft *interfaces.Draft, concepts map[string]string,
	reject func(kind, text, reason string)) {
	properties := map[string]map[string]bool{}
	if draft != nil {
		for _, ot := range draft.Items.ObjectTypes {
			if !ot.Adopted {
				continue
			}
			properties[ot.ID] = map[string]bool{}
			for _, p := range ot.Properties {
				properties[ot.ID][p.Name] = true
			}
		}
	}
	conceptIDsOf := func(ids []string) []string {
		out := []string{}
		for _, id := range ids {
			if c, ok := concepts[strings.TrimSpace(id)]; ok && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
		return out
	}
	// What executes an action type that a run dropped no longer does: the Agent may name it anew.
	followExecutors(spec)
	added := 0
	// The needs by the ids the Agent gave them, and what it said executes each, to name once all are in.
	ids := map[string]*interfaces.RequirementLogicNeed{}
	for _, n := range spec.LogicNeeds {
		ids[keptPrefix+n.ID] = n
	}
	executors := map[*interfaces.RequirementLogicNeed]string{}
	for _, p := range proposed {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		if i := slices.IndexFunc(spec.LogicNeeds, func(n *interfaces.RequirementLogicNeed) bool {
			return p.ID == keptPrefix+n.ID || n.Kind == p.Kind && sameText(n.Name, name)
		}); i >= 0 {
			kept := spec.LogicNeeds[i]
			if p.ID != "" {
				ids[p.ID] = kept
			}
			// One a person changed keeps what they said executes it.
			if !kept.Edited && kept.ExecutedBy == "" {
				executors[kept] = strings.TrimSpace(p.ExecutedBy)
			}
			for _, c := range conceptIDsOf(p.ConceptIDs) {
				if !slices.Contains(kept.ConceptIDs, c) {
					kept.ConceptIDs = append(kept.ConceptIDs, c)
				}
			}
			// A need the documents stated before there was a model is placed on it when the analysis
			// gives it again; one a person changed stays as they left it.
			if len(kept.ObjectTypeIDs) == 0 && !kept.Edited {
				placeOnModel(kept, p, properties)
			}
			continue
		}
		citations, reason := basisOf(p.Citations, p.SupplementReason, docs, source != interfaces.LOGIC_NEED_FROM_DOCUMENTS)
		switch {
		case reason != "":
		case !interfaces.IsLogicNeedKind(p.Kind):
			reason = rejectedUnknownKind
		case len(citations) > 0 && !numbersInQuotes(p.Definition, citations):
			reason = rejectedNumberNotInQuote
		case p.Kind == interfaces.LOGIC_NEED_SKILL && !namedInDocuments(name, docs):
			reason = rejectedBasisNotInDocuments
		case added == maxRunLogicNeeds:
			reason = rejectedTooMany
		}
		if reason != "" {
			reject(rejectedLogicNeed, name, reason)
			continue
		}
		added++
		need := &interfaces.RequirementLogicNeed{
			ID: nextID(spec, "n", spec.LogicNeeds, logicNeedID), Kind: p.Kind, Name: name, Definition: p.Definition,
			ObjectTypeIDs: []string{}, Properties: []interfaces.RequirementPropertyRef{},
			ConceptIDs: conceptIDsOf(p.ConceptIDs), ScenarioIDs: []string{}, Citations: citations,
			SupplementReason: p.SupplementReason, Source: source,
		}
		placeOnModel(need, p, properties)
		spec.LogicNeeds = append(spec.LogicNeeds, need)
		if p.ID != "" {
			ids[p.ID] = need
		}
		executors[need] = strings.TrimSpace(p.ExecutedBy)
	}
	for need, ref := range executors {
		if by, ok := ids[ref]; ok && by != need && need.Kind == interfaces.LOGIC_NEED_ACTION_TYPE && interfaces.CanExecute(by.Kind) {
			need.ExecutedBy = by.ID
		}
	}
	followExecutors(spec)
}

// placeOnModel gives a need the object types, properties and metric type the Agent proposed for it.
// Only adopted object types and their properties stand, by properties of each adopted object type;
// a property names its object type too.
func placeOnModel(need *interfaces.RequirementLogicNeed, p agentLogicNeed, properties map[string]map[string]bool) {
	if need.Kind == interfaces.LOGIC_NEED_METRIC && need.SuggestedMetricType == "" &&
		slices.Contains(interfaces.LogicNeedMetricTypes, p.SuggestedMetricType) {
		need.SuggestedMetricType = p.SuggestedMetricType
	}
	for _, id := range p.ObjectTypeIDs {
		if properties[id] != nil && !slices.Contains(need.ObjectTypeIDs, id) {
			need.ObjectTypeIDs = append(need.ObjectTypeIDs, id)
		}
	}
	for _, ref := range p.Properties {
		if !properties[ref.ObjectTypeID][ref.Property] {
			continue
		}
		property := interfaces.RequirementPropertyRef{ObjectTypeID: ref.ObjectTypeID, Property: ref.Property}
		if !slices.Contains(need.Properties, property) {
			need.Properties = append(need.Properties, property)
		}
		if !slices.Contains(need.ObjectTypeIDs, ref.ObjectTypeID) {
			need.ObjectTypeIDs = append(need.ObjectTypeIDs, ref.ObjectTypeID)
		}
	}
}

// AnalyseKinetic starts the kinetic run of a project; it needs the confirmed model.
func (s *requirementService) AnalyseKinetic(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, discardDecisions bool) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AnalyseRequirementKinetic")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	docs, err := s.ra.GetDocuments(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get documents failed")
		return nil, internalError(ctx, "Get documents failed", err)
	}
	if err := checkInputsSize(ctx, docs); err != nil {
		span.SetStatus(codes.Error, "Inputs too large")
		return nil, err
	}
	draft, found, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !found || !modelConfirmed(draft) {
		span.SetStatus(codes.Error, "Model not confirmed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_ModelNotConfirmed)
	}
	previous, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	items, err := s.kineticNetworkItems(ctx, token, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "Get network items failed")
		return nil, err
	}
	spec, version, read := emptySpec(), 1, (*interfaces.Requirement)(nil)
	if ok {
		spec, version, read = withLists(cloneSpec(&previous.Spec)), nextVersion(previous.Version, draft), previous
	}
	keepForKinetic(spec, discardDecisions)
	run, err := s.startRun(ctx, projectID, interfaces.REQUIREMENT_RUN_KINETIC, caller, read)
	if err != nil {
		span.SetStatus(codes.Error, "Start run failed")
		return nil, err
	}
	project := entry.Project
	byID := documentsByID(docs)
	concepts := map[string]string{}
	for _, c := range spec.Concepts {
		concepts[c.ID], concepts[keptPrefix+c.ID] = c.ID, c.ID
	}
	p := &partRun{
		kind: interfaces.REQUIREMENT_RUN_KINETIC, projectID: projectID, run: run, caller: caller,
		format: kineticResponseFormat(), newOut: func() any { return &kineticOutput{} },
		prepareMessage: func(context.Context) (string, error) {
			return buildKineticMessage(project.NamingLanguage, docs, draft, spec, items)
		},
	}
	p.check = func(_ context.Context, out any) {
		const kind = interfaces.REQUIREMENT_RUN_KINETIC
		checked := cloneSpec(spec)
		checked.Rejected = rejectedOf(checked.Rejected, kind)
		addLogicNeeds(checked, uncovered(out.(*kineticOutput).LogicNeeds, items), interfaces.LOGIC_NEED_FROM_ANALYSIS,
			byID, draft, concepts, func(k, text, reason string) {
				checked.Rejected = append(checked.Rejected, &interfaces.RequirementRejected{Kind: k, Text: text, Reason: reason, Run: kind})
			})
		// What the network has covers the needs of its name: they are linked to it, not left new.
		linkCovered(checked, items, caller, s.now())
		needScenarios(checked)
		spec = checked
		p.inputs = requirementInputsOf(docs, project)
	}
	p.write = func(req *interfaces.Requirement, _ any) {
		runs := req.Spec.Runs
		req.Spec = *spec
		req.Spec.Runs = runs
		req.Version = max(req.Version, version)
	}
	runCtx := context.WithoutCancel(ctx)
	s.run(func() { s.execute(runCtx, p) })
	span.SetStatus(codes.Ok, "")
	return s.startedRun(ctx, projectID)
}

// kineticNetworkItems are the items of the network the project started on, for the kinetic
// analysis to build on; none on a network the project created. Lists that cannot be read leave
// out their items: the analysis goes on with the rest.
func (s *requirementService) kineticNetworkItems(ctx context.Context, token string, lineageID string) (
	[]*interfaces.LogicNetworkItem, error) {
	network, released, err := logics.LiveNetworkOf(ctx, s.rla, lineageID)
	if err != nil {
		return nil, internalError(ctx, "Get live network failed", err)
	}
	running := !released && s.runsVersion(ctx, token, network)
	fromNetwork, err := s.startedOnNetwork(ctx, lineageID, released, running)
	if err != nil {
		return nil, internalError(ctx, "Get first release failed", err)
	}
	if !fromNetwork {
		return nil, nil
	}
	items, _ := s.liveReader(ctx, token, network).networkItems(nil)
	return items, nil
}
