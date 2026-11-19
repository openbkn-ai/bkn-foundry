// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package diagnosis

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/draft"
)

// The judge turns the facts into suggestions. Ask AI's suggestions are kept only as far as the facts
// bear them out: every object type, relation type, property, reference, subtype, concept and quote
// it names must be one given, a new relation needs a counted reference at the threshold or one with
// hits and a requirement quote, and a write-back object follows the decision-tracking pattern.
// Without Ask AI the suggestions come from the counts alone. Impact and rank are the server's.

const (
	titleMaxLength  = 200
	reasonMaxLength = 400
	// hitRateDefault and minRowsDefault are the reference threshold when settings give none.
	hitRateDefault = 0.6
	minRowsDefault = 10
	// wholeCodeMinRows is the rows a text code field needs when every value is a target identity: a
	// small master or event table is common, and whole containment of codes is no coincidence.
	wholeCodeMinRows = 3
	// emptyShare is the share of empty references a data quality suggestion reports from.
	emptyShare = 0.5
	// writebackMaxSnapshots bounds the basis snapshot of a write-back object: a few values only.
	writebackMaxSnapshots = 3
)

var writebackRoles = []string{interfaces.WRITEBACK_ROLE_ID, interfaces.WRITEBACK_ROLE_TRACKED,
	interfaces.WRITEBACK_ROLE_TIME, interfaces.WRITEBACK_ROLE_ACTOR, interfaces.WRITEBACK_ROLE_DECISION,
	interfaces.WRITEBACK_ROLE_SNAPSHOT, interfaces.WRITEBACK_ROLE_OUTCOME}

var writebackTypes = []string{"string", "text", "integer", "float", "decimal", "boolean", "date", "datetime"}

var propertyName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var cardinalities = []string{interfaces.DRAFT_CARDINALITY_ONE_TO_ONE, interfaces.DRAFT_CARDINALITY_ONE_TO_MANY,
	interfaces.DRAFT_CARDINALITY_MANY_TO_ONE, interfaces.DRAFT_CARDINALITY_MANY_TO_MANY}

type judge struct {
	hitRate   float64
	minRows   int64
	facts     *interfaces.DraftNetworkFacts
	items     *interfaces.DraftItems
	language  string
	questions *interfaces.ScenarioQuestions
	concepts  []conceptInput
	dismissed map[string]bool
	// unreachable are the questions an adopted path does not answer, by id.
	unreachable map[string]interfaces.ScenarioQuestion
	// refs are the references the Agent reads, by index.
	refs []interfaces.DraftReference
}

func newJudge(setting common.DraftSetting, facts *interfaces.DraftNetworkFacts, questions *interfaces.ScenarioQuestions,
	concepts []conceptInput) *judge {
	j := &judge{hitRate: setting.DiagnosisHitRate, minRows: setting.DiagnosisMinRows, facts: facts,
		items: &facts.Draft.Items, language: projecthooks.DraftLanguage(facts.Draft), questions: questions,
		concepts: concepts, dismissed: dismissedIDs(&facts.Draft.Items),
		unreachable: map[string]interfaces.ScenarioQuestion{}}
	if j.hitRate <= 0 {
		j.hitRate = hitRateDefault
	}
	if j.minRows <= 0 {
		j.minRows = minRowsDefault
	}
	if questions != nil {
		for _, q := range questions.Questions {
			if !reachable(q) {
				j.unreachable[q.ID] = q
			}
		}
	}
	facts.References = sameTargetBranches(facts.References, j.items.RelationTypes)
	// The counted first, by rate, so the message keeps the strongest when it cuts.
	j.refs = slices.Clone(facts.References)
	slices.SortStableFunc(j.refs, func(a, b interfaces.DraftReference) int {
		if a.Counted != b.Counted {
			if a.Counted {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.Rate, a.Rate)
	})
	j.refs = j.refs[:min(len(j.refs), agentMaxReferences)]
	return j
}

func reachable(q interfaces.ScenarioQuestion) bool {
	return len(q.Unconnected) == 0 && len(q.MissingItems) == 0
}

func (j *judge) object(id string) *interfaces.DraftObjectType {
	return projecthooks.FindObjectType(j.items, id)
}

func (j *judge) relation(id string) *interfaces.DraftRelationType {
	return projecthooks.FindRelationType(j.items, id)
}

// qualifies tells a reference whose counts reach the threshold. A text code field every value of
// which is a target identity needs only a few rows.
func (j *judge) qualifies(r interfaces.DraftReference) bool {
	nonEmpty := r.Rows - r.Nulls
	if !r.Counted || r.Rate < j.hitRate {
		return false
	}
	return nonEmpty >= j.minRows || nonEmpty >= wholeCodeMinRows && r.Hits == nonEmpty && j.textual(r.Source, r.SourceProperty)
}

// textual tells a property holding text, not numbers: small numbers fall into any identity by chance.
func (j *judge) textual(objectTypeID string, property string) bool {
	if ot := j.object(objectTypeID); ot != nil {
		for _, p := range ot.Properties {
			if p.Name == property {
				return p.Type == "string" || p.Type == "text"
			}
		}
	}
	return false
}

// sameTargetBranches makes one plain reference of a polymorphic field whose every branch reaches the
// same identity: the type field then tells nothing about where the code points. A relation of the
// draft that maps the field to that identity is the reference's relation, as for any plain reference.
func sameTargetBranches(refs []interfaces.DraftReference, relations []*interfaces.DraftRelationType) []interfaces.DraftReference {
	type field struct{ source, property string }
	branches := map[field][]int{}
	for i, r := range refs {
		if r.Condition != nil {
			branches[field{r.Source, r.SourceProperty}] = append(branches[field{r.Source, r.SourceProperty}], i)
		}
	}
	merged := map[int]bool{}
	out := []interfaces.DraftReference{}
	for i, r := range refs {
		if merged[i] {
			continue
		}
		ids := branches[field{r.Source, r.SourceProperty}]
		if r.Condition == nil || len(ids) < 2 || slices.ContainsFunc(ids, func(k int) bool {
			return !refs[k].Counted || refs[k].Target != r.Target || refs[k].TargetProperty != r.TargetProperty
		}) {
			out = append(out, r)
			continue
		}
		plain := r
		plain.Condition, plain.Rows, plain.Nulls, plain.Hits = nil, 0, 0, 0
		for _, k := range ids {
			merged[k] = true
			plain.Rows += refs[k].Rows
			plain.Nulls += refs[k].Nulls
			plain.Hits += refs[k].Hits
		}
		if nonEmpty := plain.Rows - plain.Nulls; nonEmpty > 0 {
			plain.Rate = float64(plain.Hits) / float64(nonEmpty)
		}
		mapping := interfaces.DraftFieldMapping{SourceProperty: plain.SourceProperty, TargetProperty: plain.TargetProperty}
		for _, rt := range relations {
			if rt != nil && rt.LinkTable == nil && rt.Source == plain.Source && rt.Target == plain.Target &&
				len(rt.Mapping) == 1 && rt.Mapping[0] == mapping {
				plain.RelationTypeID = rt.ID
				break
			}
		}
		out = append(out, plain)
	}
	return out
}

// relationName is Ask AI's name when it is a name: not empty, not too long, no title with arrows.
func relationName(name string, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > interfaces.DRAFT_NAME_MAX_LENGTH || strings.ContainsAny(name, "→—") ||
		strings.Contains(name, "->") {
		return fallback
	}
	return name
}

// covered tells a reference an adopted relation of the draft maps.
func (j *judge) covered(r interfaces.DraftReference) bool {
	rt := j.relation(r.RelationTypeID)
	return rt != nil && rt.Adopted
}

func (j *judge) metrics() interfaces.DiagnosisMetrics {
	m := interfaces.DiagnosisMetrics{Connectivity: connectivityOf(j.facts.Components)}
	mapped := map[string]bool{}
	for _, r := range j.facts.References {
		if r.RelationTypeID != "" {
			mapped[r.RelationTypeID] = true
		}
		if !j.qualifies(r) || !projecthooks.ObjectTypeAdopted(j.items, r.Source) ||
			!projecthooks.ObjectTypeAdopted(j.items, r.Target) {
			continue
		}
		m.References.Total++
		if j.covered(r) || j.dismissed[j.referenceSuggestionID(r)] {
			m.References.Covered++
		}
	}
	// An adopted relation the scan did not see, as on a field not named as a code, is a covered reference too.
	for _, rt := range j.items.RelationTypes {
		if rt.Adopted && rt.LinkTable == nil && len(rt.Mapping) == 1 && rt.Source != rt.Target && !mapped[rt.ID] &&
			rt.DocumentMapping == nil {
			m.References.Total++
			m.References.Covered++
		}
	}
	m.References.NotCounted = j.facts.NotCounted
	if j.questions != nil {
		m.Reachability.Total = len(j.questions.Questions)
		m.Reachability.Reachable = m.Reachability.Total - len(j.unreachable)
	}
	for _, c := range j.concepts {
		if c.Category == "object" || c.Category == "event" {
			m.PendingConcepts++
		}
	}
	return m
}

func connectivityOf(components [][]string) interfaces.DiagnosisConnectivity {
	c := interfaces.DiagnosisConnectivity{Components: len(components)}
	for i, component := range components {
		if i == 0 {
			c.Largest = len(component)
		}
		c.Objects += len(component)
	}
	return c
}

func (j *judge) agentInput() agentInput {
	in := agentInput{Language: j.language, Kind: agentKind, Components: j.facts.Components,
		ObjectTypes: []agentObjectType{}, RelationTypes: []agentRelationType{}, References: []agentReference{},
		Subtypes: []agentSubtype{}, NotCounted: j.facts.NotCounted, UnreachableQuestions: []agentQuestion{},
		Concepts: j.concepts[:min(len(j.concepts), agentMaxConcepts)]}
	for _, ot := range j.items.ObjectTypes {
		o := agentObjectType{ID: ot.ID, Name: ot.Name, Aliases: ot.Aliases, Description: ot.Description,
			Table: ot.Table.Name, Adopted: ot.Adopted, NeedsIdentity: ot.NeedsIdentity, Identity: ot.PrimaryKeys,
			DisplayKey: ot.DisplayKey, Properties: []agentProperty{}}
		for _, p := range ot.Properties {
			o.Properties = append(o.Properties, agentProperty{Name: p.Name, DisplayName: p.DisplayName, Type: p.Type,
				MappedField: p.MappedField, Description: p.Description})
		}
		in.ObjectTypes = append(in.ObjectTypes, o)
	}
	for _, rt := range j.items.RelationTypes {
		r := agentRelationType{ID: rt.ID, Name: rt.Name, Source: rt.Source, Target: rt.Target, Adopted: rt.Adopted,
			Mapping: rt.Mapping}
		if rt.Evidence.Computed {
			rate := rt.Evidence.HitRate
			r.HitRate = &rate
		}
		in.RelationTypes = append(in.RelationTypes, r)
	}
	for i, r := range j.refs {
		in.References = append(in.References, agentReference{Index: i, Source: r.Source, SourceProperty: r.SourceProperty,
			Target: r.Target, TargetProperty: r.TargetProperty, Condition: r.Condition, Self: r.Self, Counted: r.Counted,
			Rows: r.Rows, Nulls: r.Nulls, Hits: r.Hits, Rate: r.Rate, RelationTypeID: r.RelationTypeID,
			Adopted: j.covered(r)})
	}
	for i, st := range j.facts.Subtypes {
		in.Subtypes = append(in.Subtypes, agentSubtype{Index: i, DraftSubtype: st})
	}
	if j.questions != nil {
		for _, q := range j.questions.Questions {
			if _, ok := j.unreachable[q.ID]; ok {
				in.UnreachableQuestions = append(in.UnreachableQuestions, agentQuestion{ID: q.ID, Text: q.Text,
					Unconnected: q.Unconnected})
			}
		}
	}
	return in
}

// suggestions are Ask AI's, checked, or the server's own when there is no result or none of it
// holds; the data quality of the references is always the server's. ai tells which.
// A counted fact is never lost to Ask AI leaving it out: the server's own suggestions that rest on the
// data alone (relations, identities, adoptions) follow Ask AI's, which win where both make the same one.
// Leaving an object out is a judgment, so it stays Ask AI's once Ask AI answered.
func (j *judge) suggestions(out *agentOutput) ([]*interfaces.DiagnosisSuggestion, bool) {
	list := []*interfaces.DiagnosisSuggestion{}
	ai := false
	if out != nil {
		for _, a := range out.Suggestions[:min(len(out.Suggestions), agentMaxSuggestions)] {
			if s := j.checked(a); s != nil {
				list = append(list, s)
			}
		}
		ai = len(list) > 0 || len(out.Suggestions) == 0
	}
	for _, s := range j.fallback() {
		if !ai || s.Kind != interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT {
			list = append(list, s)
		}
	}
	list = append(list, j.dataQuality()...)
	return j.ranked(list), ai
}

// ranked drops repeated and dismissed suggestions, works out each one's impact and ranks them by
// the objects it connects, the questions it makes answerable and its evidence.
func (j *judge) ranked(list []*interfaces.DiagnosisSuggestion) []*interfaces.DiagnosisSuggestion {
	seen := map[string]bool{}
	kept := []*interfaces.DiagnosisSuggestion{}
	scores := map[string]float64{}
	for _, s := range list {
		if seen[s.ID] || j.dismissed[s.ID] {
			continue
		}
		seen[s.ID] = true
		if s.Impact.Connects == nil {
			s.Impact.Connects = []string{}
		}
		if s.Impact.Questions == nil {
			s.Impact.Questions = []string{}
		}
		strength := s.Evidence.Rate
		if strength == 0 && len(s.Evidence.Quotes) > 0 {
			strength = 0.5
		}
		scores[s.ID] = 2*float64(len(s.Impact.Connects)) + 3*float64(len(s.Impact.Questions)) + strength
		if s.Kind == interfaces.DIAGNOSIS_KIND_DATA_QUALITY {
			scores[s.ID] = -1
		}
		kept = append(kept, s)
	}
	slices.SortStableFunc(kept, func(a, b *interfaces.DiagnosisSuggestion) int {
		return cmp.Compare(scores[b.ID], scores[a.ID])
	})
	for i, s := range kept {
		s.Rank = i + 1
	}
	return kept
}

// suggestionID is a stable hash of a suggestion's kind and what it changes.
func suggestionID(kind string, key ...string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.Join(key, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func (j *judge) referenceSuggestionID(r interfaces.DraftReference) string {
	condition := ""
	if r.Condition != nil {
		condition = r.Condition.Property + "=" + r.Condition.Value
	}
	return suggestionID(interfaces.DIAGNOSIS_KIND_CREATE_RELATION, r.Source, r.SourceProperty, r.Target,
		r.TargetProperty, condition)
}

func payload(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	raw, _ := json.Marshal(v)
	return raw
}

func groupOf(kind string) string {
	switch kind {
	case interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, interfaces.DIAGNOSIS_KIND_CREATE_RELATION:
		return interfaces.DIAGNOSIS_GROUP_CONNECT
	case interfaces.DIAGNOSIS_KIND_DATA_QUALITY:
		return interfaces.DIAGNOSIS_GROUP_DATA
	}
	return interfaces.DIAGNOSIS_GROUP_OBJECT
}

func newSuggestion(kind string, id string, title string, reason string, apply any) *interfaces.DiagnosisSuggestion {
	return &interfaces.DiagnosisSuggestion{ID: id, Kind: kind, Group: groupOf(kind), Title: truncate(title, titleMaxLength),
		Reason: truncate(reason, reasonMaxLength), Apply: payload(apply), Status: interfaces.DIAGNOSIS_STATUS_OPEN,
		Impact: interfaces.DiagnosisImpact{Connects: []string{}, Questions: []string{}}}
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// impact is what joining the given object types, and adopting those added, does to the network: the
// adopted object types it brings into a component they were not in, and the unreachable questions
// whose every unconnected pair it joins.
func (j *judge) impact(added []string, edges [][2]string) interfaces.DiagnosisImpact {
	parent := map[string]string{}
	var find func(string) string
	find = func(id string) string {
		if parent[id] != id {
			parent[id] = find(parent[id])
		}
		return parent[id]
	}
	before := map[string]int{}
	for i, component := range j.facts.Components {
		for _, id := range component {
			parent[id] = component[0]
			before[id] = i
		}
	}
	for _, id := range added {
		if _, ok := parent[id]; !ok {
			parent[id] = id
			before[id] = -1
		}
	}
	for _, e := range edges {
		_, a := parent[e[0]]
		_, b := parent[e[1]]
		if a && b {
			parent[find(e[0])] = find(e[1])
		}
	}
	impact := interfaces.DiagnosisImpact{Connects: []string{}, Questions: []string{}}
	// Each merged group keeps its largest component as it was; the rest joined it.
	largest := map[string]int{}
	for id, i := range before {
		root := find(id)
		if best, ok := largest[root]; !ok || i >= 0 && (best < 0 || i < best) {
			largest[root] = i
		}
	}
	for _, component := range j.facts.Components {
		for _, id := range component {
			if before[id] != largest[find(id)] {
				impact.Connects = append(impact.Connects, id)
			}
		}
	}
	for _, id := range added {
		if before[id] < 0 && largest[find(id)] >= 0 {
			impact.Connects = append(impact.Connects, id)
		}
	}
	if len(edges) > 0 && j.questions != nil {
		for _, q := range j.questions.Questions {
			u, ok := j.unreachable[q.ID]
			if !ok || len(u.MissingItems) > 0 || len(u.Unconnected) == 0 {
				continue
			}
			if !slices.ContainsFunc(u.Unconnected, func(p interfaces.ScenarioUnconnected) bool {
				_, a := parent[p.From]
				_, b := parent[p.To]
				return !a || !b || find(p.From) != find(p.To)
			}) {
				impact.Questions = append(impact.Questions, q.ID)
			}
		}
	}
	return impact
}

// questionsOf keeps the unreachable questions Ask AI names, for a suggestion that joins nothing.
func (j *judge) questionsOf(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if _, ok := j.unreachable[id]; ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// pending is the object type left out with the relations its adoption brings in, nil for another.
func (j *judge) pending(id string) *interfaces.DraftPendingObject {
	for i := range j.facts.Pending {
		if j.facts.Pending[i].ObjectTypeID == id {
			return &j.facts.Pending[i]
		}
	}
	return nil
}

func (j *judge) adoptionEdges(p *interfaces.DraftPendingObject) [][2]string {
	edges := [][2]string{}
	for _, id := range p.Relations {
		if rt := j.relation(id); rt != nil {
			edges = append(edges, [2]string{rt.Source, rt.Target})
		}
	}
	return edges
}

// The suggestions, kind by kind, from the facts; text is the server's when Ask AI gave none.

func (j *judge) createRelation(r interfaces.DraftReference, name string, description string, cardinality string,
	quotes []interfaces.DiagnosisQuote) *interfaces.DiagnosisSuggestion {
	source, target := j.object(r.Source), j.object(r.Target)
	name = relationName(name, r.Name)
	if !slices.Contains(cardinalities, cardinality) {
		cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_ONE
	}
	if strings.TrimSpace(description) == "" {
		description = j.text("relation_description", source.Name, target.Name, j.display(source, r.SourceProperty))
	}
	if r.Condition != nil && !strings.Contains(description, r.Condition.Value) {
		description += j.text("condition", j.display(source, r.Condition.Property), r.Condition.Value)
	}
	apply := interfaces.DiagnosisCreateRelation{Source: r.Source, Target: r.Target,
		Mapping: []interfaces.DraftFieldMapping{{SourceProperty: r.SourceProperty, TargetProperty: r.TargetProperty}},
		Name:    strings.TrimSpace(name), Description: truncate(description, interfaces.DRAFT_DESCRIPTION_MAX_LENGTH),
		Cardinality: cardinality}
	title := source.Name + " —" + apply.Name + "→ " + target.Name
	if r.Condition != nil {
		title += j.text("condition_title", j.display(source, r.Condition.Property), r.Condition.Value)
	}
	nonEmpty := r.Rows - r.Nulls
	reason := j.text("reference_reason", j.display(source, r.SourceProperty), r.Hits, nonEmpty, percent(r.Rate),
		target.Name, j.display(target, r.TargetProperty))
	s := newSuggestion(interfaces.DIAGNOSIS_KIND_CREATE_RELATION, j.referenceSuggestionID(r), title, reason, apply)
	s.Evidence = interfaces.DiagnosisEvidence{Rows: r.Rows, Hits: r.Hits, Rate: r.Rate, Nulls: r.Nulls,
		SourceField: mappedField(source, r.SourceProperty), TargetField: mappedField(target, r.TargetProperty),
		Quotes: quotes, NotCounted: !r.Counted}
	if r.Condition != nil {
		s.Evidence.Condition = &interfaces.DiagnosisCondition{Field: mappedField(source, r.Condition.Property),
			Value: r.Condition.Value}
	}
	added := []string{}
	for _, id := range []string{r.Source, r.Target} {
		if p := j.pending(id); p != nil && !p.NeedsIdentity {
			added = append(added, id)
		}
	}
	s.Impact = j.impact(added, [][2]string{{r.Source, r.Target}})
	return s
}

func (j *judge) subtypeRelation(st interfaces.DraftSubtype, name string, description string) *interfaces.DiagnosisSuggestion {
	sub, super := j.object(st.Sub), j.object(st.Super)
	name = relationName(name, j.text("subtype_name", super.Name))
	if strings.TrimSpace(description) == "" {
		description = j.text("subtype_description", sub.Name, super.Name)
	}
	apply := interfaces.DiagnosisCreateRelation{Source: st.Sub, Target: st.Super,
		Mapping: []interfaces.DraftFieldMapping{{SourceProperty: st.SubProperty, TargetProperty: st.SuperProperty}},
		Name:    strings.TrimSpace(name), Description: truncate(description, interfaces.DRAFT_DESCRIPTION_MAX_LENGTH),
		Cardinality: interfaces.DRAFT_CARDINALITY_ONE_TO_ONE}
	s := newSuggestion(interfaces.DIAGNOSIS_KIND_CREATE_RELATION,
		suggestionID(interfaces.DIAGNOSIS_KIND_CREATE_RELATION, st.Sub, st.SubProperty, st.Super, st.SuperProperty, "subtype"),
		sub.Name+" —"+apply.Name+"→ "+super.Name, j.text("subtype_reason", sub.Name, st.Hits, super.Name), apply)
	s.Evidence = interfaces.DiagnosisEvidence{Rows: st.Rows, Hits: st.Hits, Rate: 1,
		SourceField: mappedField(sub, st.SubProperty), TargetField: mappedField(super, st.SuperProperty)}
	s.Impact = j.impact(nil, [][2]string{{st.Sub, st.Super}})
	return s
}

func (j *judge) adoptRelation(rt *interfaces.DraftRelationType) *interfaces.DiagnosisSuggestion {
	source, target := j.object(rt.Source), j.object(rt.Target)
	reason := j.text("adopt_relation_reason")
	if rt.Evidence.Computed {
		reason = j.text("adopt_relation_counted", rt.Evidence.Hits, rt.Evidence.Rows-rt.Evidence.Nulls, percent(rt.Evidence.HitRate))
	}
	s := newSuggestion(interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, suggestionID(interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, rt.ID),
		j.text("adopt", source.Name+" —"+rt.Name+"→ "+target.Name), reason,
		interfaces.DiagnosisAdoptRelation{RelationTypeID: rt.ID})
	if rt.Evidence.Computed {
		s.Evidence = interfaces.DiagnosisEvidence{Rows: rt.Evidence.Rows, Hits: rt.Evidence.Hits, Rate: rt.Evidence.HitRate,
			Nulls: rt.Evidence.Nulls}
		if len(rt.Mapping) == 1 {
			s.Evidence.SourceField = mappedField(source, rt.Mapping[0].SourceProperty)
			s.Evidence.TargetField = mappedField(target, rt.Mapping[0].TargetProperty)
		}
	}
	s.Impact = j.impact(nil, [][2]string{{rt.Source, rt.Target}})
	return s
}

func (j *judge) adoptObject(p *interfaces.DraftPendingObject) *interfaces.DiagnosisSuggestion {
	ot := j.object(p.ObjectTypeID)
	if p.NeedsIdentity {
		candidates := p.Candidates
		if candidates == nil {
			candidates = [][]string{}
		}
		shown := []string{}
		for _, c := range candidates {
			names := []string{}
			for _, name := range c {
				names = append(names, j.display(ot, name))
			}
			shown = append(shown, strings.Join(names, " + "))
		}
		reason := j.text("identity_reason", len(p.Relations))
		if len(shown) > 0 {
			reason += j.text("identity_candidates", strings.Join(shown, j.text("or")))
		}
		s := newSuggestion(interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY,
			suggestionID(interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY, ot.ID), j.text("identity_title", ot.Name), reason,
			interfaces.DiagnosisChooseIdentity{ObjectTypeID: ot.ID, Candidates: candidates})
		s.Impact = j.impact([]string{ot.ID}, j.adoptionEdges(p))
		return s
	}
	s := newSuggestion(interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT, suggestionID(interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT, ot.ID),
		j.text("adopt", ot.Name), j.text("adopt_object_reason", len(p.Relations)),
		interfaces.DiagnosisObject{ObjectTypeID: ot.ID})
	s.Impact = j.impact([]string{ot.ID}, j.adoptionEdges(p))
	return s
}

func (j *judge) excludeObject(ot *interfaces.DraftObjectType, reason string) *interfaces.DiagnosisSuggestion {
	if strings.TrimSpace(reason) == "" {
		reason = j.text("exclude_reason")
	}
	return newSuggestion(interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, suggestionID(interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, ot.ID),
		j.text("exclude_title", ot.Name), reason, interfaces.DiagnosisObject{ObjectTypeID: ot.ID})
}

// fallback are the server's own suggestions: counted references at the threshold, subtypes, drafted
// relations and object types waiting to be adopted, and the adopted object types nothing connects.
func (j *judge) fallback() []*interfaces.DiagnosisSuggestion {
	list := []*interfaces.DiagnosisSuggestion{}
	touched := map[string]bool{}
	for _, r := range j.facts.References {
		if !j.qualifies(r) || r.RelationTypeID != "" || j.object(r.Source) == nil || j.object(r.Target) == nil ||
			j.redundant(r) {
			continue
		}
		list = append(list, j.createRelation(r, "", "", "", nil))
		touched[r.Source], touched[r.Target] = true, true
	}
	for _, st := range j.facts.Subtypes {
		if j.object(st.Sub).Adopted && j.object(st.Super).Adopted && !j.subtypeLinked(st) {
			list = append(list, j.subtypeRelation(st, "", ""))
			touched[st.Sub], touched[st.Super] = true, true
		}
	}
	for _, rt := range j.items.RelationTypes {
		if !rt.Adopted && projecthooks.ObjectTypeAdopted(j.items, rt.Source) && projecthooks.ObjectTypeAdopted(j.items, rt.Target) &&
			rt.LinkTable == nil && rt.DocumentMapping == nil && rt.Evidence.Computed && rt.Evidence.HitRate >= j.hitRate {
			list = append(list, j.adoptRelation(rt))
			touched[rt.Source], touched[rt.Target] = true, true
		}
	}
	for i := range j.facts.Pending {
		if p := &j.facts.Pending[i]; len(p.Relations) > 0 {
			list = append(list, j.adoptObject(p))
			for _, e := range j.adoptionEdges(p) {
				touched[e[0]], touched[e[1]] = true, true
			}
		}
	}
	for _, component := range j.facts.Components {
		if ot := j.object(component[0]); len(component) == 1 && len(j.facts.Components) > 1 && !touched[ot.ID] && !ot.Existing &&
			!j.hasLeads(ot.ID) {
			list = append(list, j.excludeObject(ot, ""))
		}
	}
	return list
}

// dataQuality reports the references a relation stands for, or a suggestion makes, that most of the
// source's records leave empty: the relation holds, the trace breaks at those records.
func (j *judge) dataQuality() []*interfaces.DiagnosisSuggestion {
	list := []*interfaces.DiagnosisSuggestion{}
	seen := map[string]bool{}
	for _, r := range j.facts.References {
		if !r.Counted || r.Rate < j.hitRate || r.FieldRows < j.minRows || float64(r.FieldNulls) < emptyShare*float64(r.FieldRows) ||
			seen[r.Source+"|"+r.SourceProperty] {
			continue
		}
		seen[r.Source+"|"+r.SourceProperty] = true
		source := j.object(r.Source)
		s := newSuggestion(interfaces.DIAGNOSIS_KIND_DATA_QUALITY,
			suggestionID(interfaces.DIAGNOSIS_KIND_DATA_QUALITY, r.Source, r.SourceProperty),
			j.text("quality_title", r.FieldNulls, r.FieldRows, source.Name, j.display(source, r.SourceProperty)),
			j.text("quality_reason"), nil)
		s.Evidence = interfaces.DiagnosisEvidence{Rows: r.FieldRows, Nulls: r.FieldNulls,
			SourceField: mappedField(source, r.SourceProperty)}
		list = append(list, s)
	}
	return list
}

// checked is a suggestion of Ask AI as far as the facts bear it out, nil when they do not.
func (j *judge) checked(a agentSuggestion) *interfaces.DiagnosisSuggestion {
	var s *interfaces.DiagnosisSuggestion
	quotes := j.quotes(a.Quotes)
	switch a.Kind {
	case interfaces.DIAGNOSIS_KIND_CREATE_RELATION:
		switch {
		case a.Subtype != nil:
			if *a.Subtype < 0 || *a.Subtype >= len(j.facts.Subtypes) || j.subtypeLinked(j.facts.Subtypes[*a.Subtype]) {
				return nil
			}
			s = j.subtypeRelation(j.facts.Subtypes[*a.Subtype], a.Name, a.Description)
		case a.Reference != nil:
			if *a.Reference < 0 || *a.Reference >= len(j.refs) {
				return nil
			}
			r := j.refs[*a.Reference]
			if j.covered(r) || j.redundant(r) || !(j.qualifies(r) || r.Counted && r.Hits > 0 && len(quotes) > 0) {
				return nil
			}
			if rt := j.relation(r.RelationTypeID); rt != nil && r.Condition == nil {
				// The draft has the relation already: adopting it is the suggestion.
				if !projecthooks.ObjectTypeAdopted(j.items, rt.Source) || !projecthooks.ObjectTypeAdopted(j.items, rt.Target) {
					return nil
				}
				s = j.adoptRelation(rt)
				break
			}
			cardinality := a.Cardinality
			if r.Self && cardinality == "" {
				cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_ONE
			}
			s = j.createRelation(r, a.Name, a.Description, cardinality, quotes)
		default:
			return nil
		}
	case interfaces.DIAGNOSIS_KIND_ADOPT_RELATION:
		rt := j.relation(a.RelationTypeID)
		if rt == nil || rt.Adopted || !projecthooks.ObjectTypeAdopted(j.items, rt.Source) ||
			!projecthooks.ObjectTypeAdopted(j.items, rt.Target) {
			return nil
		}
		s = j.adoptRelation(rt)
	case interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT, interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY:
		p := j.pending(a.ObjectTypeID)
		if p == nil || p.NeedsIdentity != (a.Kind == interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY) {
			return nil
		}
		s = j.adoptObject(p)
	case interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT:
		ot := j.object(a.ObjectTypeID)
		if ot == nil || !ot.Adopted || ot.Existing || j.hasLeads(ot.ID) {
			return nil
		}
		s = j.excludeObject(ot, a.Reason)
	case interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT:
		concept := j.concept(a.ConceptID)
		if concept == nil || a.Writeback == nil || !j.writebackHolds(a.Writeback) {
			return nil
		}
		s = newSuggestion(a.Kind, suggestionID(a.Kind, concept.ID), a.Title, a.Reason, *a.Writeback)
		s.Evidence.Quotes = quotes
		if len(quotes) == 0 {
			s.Evidence.Quotes = concept.Quotes[:min(len(concept.Quotes), 2)]
		}
		s.Impact.Questions = j.questionsOf(a.Questions)
		return s
	case interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION:
		concept := j.concept(a.ConceptID)
		if concept == nil || !slices.Contains([]string{interfaces.DIAGNOSIS_DISPOSITION_PROPERTY,
			interfaces.DIAGNOSIS_DISPOSITION_KINETIC, interfaces.DIAGNOSIS_DISPOSITION_NOT_ONTOLOGY}, a.Disposition) ||
			a.Disposition == concept.Category {
			return nil
		}
		apply := interfaces.DiagnosisConceptDisposition{ConceptID: concept.ID, Disposition: a.Disposition}
		if a.Disposition == interfaces.DIAGNOSIS_DISPOSITION_PROPERTY && a.OwnerObjectTypeID != "" {
			if ot := j.object(a.OwnerObjectTypeID); ot == nil || !ot.Adopted {
				return nil
			}
			apply.OwnerObjectTypeID = a.OwnerObjectTypeID
		}
		s = newSuggestion(a.Kind, suggestionID(a.Kind, concept.ID), a.Title, a.Reason, apply)
		s.Evidence.Quotes = quotes
		return s
	default:
		return nil
	}
	// Ask AI's wording replaces the server's; the evidence and impact stay the server's. A relation's
	// title is its source, name and target as applied, so it stays the server's.
	if t := truncate(a.Title, titleMaxLength); t != "" && s.Kind != interfaces.DIAGNOSIS_KIND_CREATE_RELATION &&
		s.Kind != interfaces.DIAGNOSIS_KIND_ADOPT_RELATION {
		s.Title = t
	}
	if r := truncate(a.Reason, reasonMaxLength); r != "" {
		s.Reason = r
	}
	if len(quotes) > 0 {
		s.Evidence.Quotes = quotes
	}
	return s
}

// kinds tells two object types one of which is a subtype of the other.
func (j *judge) kinds(a string, b string) bool {
	return slices.ContainsFunc(j.facts.Subtypes, func(st interfaces.DraftSubtype) bool {
		return st.Sub == a && st.Super == b || st.Sub == b && st.Super == a
	})
}

// redundant tells a reference whose field already reaches a subtype or supertype of its target: through
// a relation of the draft, or through another reference to a subtype, the more specific. The subtype
// relation joins the two; a second relation through the same field would only repeat it.
func (j *judge) redundant(r interfaces.DraftReference) bool {
	for _, rt := range j.items.RelationTypes {
		if rt.Source == r.Source && rt.Target != r.Target && len(rt.Mapping) == 1 &&
			rt.Mapping[0].SourceProperty == r.SourceProperty && j.kinds(rt.Target, r.Target) {
			return true
		}
	}
	return slices.ContainsFunc(j.facts.References, func(o interfaces.DraftReference) bool {
		return o.Source == r.Source && o.SourceProperty == r.SourceProperty && o.Target != r.Target &&
			j.qualifies(o) && slices.ContainsFunc(j.facts.Subtypes, func(st interfaces.DraftSubtype) bool {
			return st.Sub == o.Target && st.Super == r.Target
		})
	})
}

// subtypeLinked tells a subtype the draft already joins to its supertype by their identities.
func (j *judge) subtypeLinked(st interfaces.DraftSubtype) bool {
	return slices.ContainsFunc(j.items.RelationTypes, func(rt *interfaces.DraftRelationType) bool {
		return rt.Source == st.Sub && rt.Target == st.Super && len(rt.Mapping) == 1 &&
			rt.Mapping[0] == interfaces.DraftFieldMapping{SourceProperty: st.SubProperty, TargetProperty: st.SuperProperty}
	})
}

// hasLeads tells an object type the scan left pairs uncounted for, or that a reference reaches: what
// it connects to is not settled, so it is never offered to leave out.
func (j *judge) hasLeads(id string) bool {
	return slices.Contains(j.facts.Uncounted, id) || slices.ContainsFunc(j.facts.References, func(r interfaces.DraftReference) bool {
		return r.Source != r.Target && (r.Source == id || r.Target == id)
	})
}

func (j *judge) concept(id string) *conceptInput {
	for i := range j.concepts {
		if j.concepts[i].ID == id {
			return &j.concepts[i]
		}
	}
	return nil
}

// quotes keeps the quotes that are passages of the requirement concepts given, as cited.
func (j *judge) quotes(quotes []interfaces.DiagnosisQuote) []interfaces.DiagnosisQuote {
	out := []interfaces.DiagnosisQuote{}
	for _, q := range quotes {
		text := strings.TrimSpace(q.Text)
		if text == "" {
			continue
		}
		if slices.ContainsFunc(j.concepts, func(c conceptInput) bool {
			return slices.ContainsFunc(c.Quotes, func(given interfaces.DiagnosisQuote) bool {
				return given.DocumentID == q.DocumentID && strings.Contains(given.Text, text)
			})
		}) {
			out = append(out, interfaces.DiagnosisQuote{DocumentID: q.DocumentID, Text: text})
		}
	}
	return out
}

// writebackHolds tells a write-back object of the decision-tracking pattern: its own identity, the
// tracked object's identity, a lifecycle time, an actor and a decision, a minimal basis snapshot,
// properties of known types and unique names that copy none of the tracked object's own attributes,
// and one add action and modify actions whose parameters are its properties.
func (j *judge) writebackHolds(w *interfaces.DiagnosisWriteback) bool {
	tracked := j.object(w.TrackedObjectTypeID)
	name := strings.TrimSpace(w.Name)
	if tracked == nil || !tracked.Adopted || name == "" || utf8.RuneCountInString(name) > interfaces.DRAFT_NAME_MAX_LENGTH ||
		utf8.RuneCountInString(w.Description) > interfaces.DRAFT_DESCRIPTION_MAX_LENGTH ||
		slices.ContainsFunc(j.items.ObjectTypes, func(o *interfaces.DraftObjectType) bool { return o.Name == name }) {
		return false
	}
	trackedNames := map[string]bool{}
	for _, p := range tracked.Properties {
		trackedNames[strings.ToLower(p.Name)], trackedNames[p.DisplayName] = true, true
	}
	roles := map[string]int{}
	names := map[string]bool{}
	for _, p := range w.Properties {
		if !propertyName.MatchString(p.Name) || names[p.Name] || strings.TrimSpace(p.DisplayName) == "" ||
			!slices.Contains(writebackTypes, p.Type) || !slices.Contains(writebackRoles, p.Role) {
			return false
		}
		// Only the reference to the tracked object and the basis snapshot may echo what it holds.
		if p.Role != interfaces.WRITEBACK_ROLE_TRACKED && p.Role != interfaces.WRITEBACK_ROLE_SNAPSHOT &&
			(trackedNames[p.Name] || trackedNames[p.DisplayName]) {
			return false
		}
		names[p.Name] = true
		roles[p.Role]++
	}
	if roles[interfaces.WRITEBACK_ROLE_ID] != 1 || roles[interfaces.WRITEBACK_ROLE_TRACKED] != 1 ||
		roles[interfaces.WRITEBACK_ROLE_TIME] == 0 || roles[interfaces.WRITEBACK_ROLE_ACTOR] == 0 ||
		roles[interfaces.WRITEBACK_ROLE_DECISION] == 0 || roles[interfaces.WRITEBACK_ROLE_SNAPSHOT] > writebackMaxSnapshots {
		return false
	}
	adds := 0
	for _, a := range w.Actions {
		if strings.TrimSpace(a.Name) == "" || len(a.Parameters) == 0 ||
			slices.ContainsFunc(a.Parameters, func(p string) bool { return !names[p] }) {
			return false
		}
		switch a.Kind {
		case interfaces.WRITEBACK_ACTION_ADD:
			adds++
		case interfaces.WRITEBACK_ACTION_MODIFY:
		default:
			return false
		}
	}
	return adds == 1
}

// display is a property's display name, or its name when it has none.
func (j *judge) display(ot *interfaces.DraftObjectType, property string) string {
	if p := projecthooks.FindProperty(ot, property); p != nil && p.DisplayName != "" {
		return p.DisplayName
	}
	return property
}

func mappedField(ot *interfaces.DraftObjectType, property string) string {
	if p := projecthooks.FindProperty(ot, property); p != nil {
		return p.MappedField
	}
	return ""
}

func percent(rate float64) string {
	return fmt.Sprintf("%.0f%%", rate*100)
}

func (j *judge) text(key string, args ...any) string {
	language := j.language
	if _, ok := locale.DiagnosisTexts[language]; !ok {
		language = interfaces.NAMING_LANGUAGE_ZH_CN
	}
	if len(args) == 0 {
		return locale.DiagnosisTexts[language][key]
	}
	return fmt.Sprintf(locale.DiagnosisTexts[language][key], args...)
}

// componentsOf recomputes the components of a draft, as a diagnosis keeps them current after its
// own apply.
func componentsOf(items *interfaces.DraftItems) [][]string {
	return draft.NetworkComponents(items)
}
