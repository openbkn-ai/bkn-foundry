// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
)

// The facts of a network diagnosis are computed, never guessed: the connected components of the
// adopted network, what adopting each object type left out would bring in, and an inclusion scan.
// The scan counts, over the full table, how many values of each code-like field are values of
// another object type's single identity, whatever the field is called; a type field beside a code
// field is a polymorphic reference, counted per type value; a parent field is counted against the
// object type's own identity; an identity whose every value is another's is a subtype. The scan
// counts within a budget of pairs and time, the likelier pairs first; the rest are reported as not
// counted.

const (
	// diagnosisTypeValues bounds the type values a polymorphic reference is counted for.
	diagnosisTypeValues = 5
	// diagnosisPairsDefault and diagnosisTimeoutDefault are the scan budget when settings give none.
	diagnosisPairsDefault   = 600
	diagnosisTimeoutDefault = 2 * time.Minute
)

// diagnosisIntegerRate is the rate a reference between integer fields needs when the field's name does
// not say its target: small integer identities coincide easily.
const diagnosisIntegerRate = 0.95

// networkCounter is a counter that also reads the values of a type field with how many records hold
// each, and counts a join over the records whose type field holds one value.
type networkCounter interface {
	counter
	typeValues(ctx context.Context, table *interfaces.VegaTable, field string, limit int) ([]string, error)
	joinStatsWhere(ctx context.Context, source *interfaces.VegaTable, field string, target *interfaces.VegaTable,
		key string, typeField string, value string) (rows, nulls, orphans, distinct int64, err error)
}

// typeValues are the most frequent non-null values of a type field: a few code values, like those of
// a code table.
func (c *vegaCounter) typeValues(ctx context.Context, table *interfaces.VegaTable, field string,
	limit int) ([]string, error) {
	f := quote(field)
	rows, err := c.va.Query(ctx, c.account, fmt.Sprintf(
		"SELECT %s AS type_value, COUNT(*) AS n FROM %s WHERE %s IS NOT NULL GROUP BY %s ORDER BY n DESC LIMIT %d",
		f, ref(table), f, f, limit), c.timeoutSeconds)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if v := text(row["type_value"]); strings.TrimSpace(v) != "" {
			values = append(values, v)
		}
	}
	return values, nil
}

// joinStatsWhere is joinStats over the source records whose type field holds value.
func (c *vegaCounter) joinStatsWhere(ctx context.Context, source *interfaces.VegaTable, field string,
	target *interfaces.VegaTable, key string, typeField string, value string) (int64, int64, int64, int64, error) {
	f, k := quote(field), quote(key)
	row, err := c.one(ctx, fmt.Sprintf(
		"SELECT COUNT(*) AS total, "+
			"SUM(CASE WHEN s.%s IS NULL THEN 1 ELSE 0 END) AS nulls, "+
			"SUM(CASE WHEN s.%s IS NOT NULL AND t.%s IS NULL THEN 1 ELSE 0 END) AS orphans, "+
			"COUNT(DISTINCT s.%s) AS distinct_values "+
			"FROM %s s LEFT JOIN (SELECT DISTINCT %s FROM %s) t ON s.%s = t.%s WHERE s.%s = %s",
		f, f, k, f, ref(source), k, ref(target), f, k, quote(typeField), literal(value)))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return number(row["total"]), number(row["nulls"]), number(row["orphans"]), number(row["distinct_values"]), nil
}

func (s *draftService) NetworkFacts(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.DraftNetworkFacts, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "NetworkFacts")
	defer span.End()

	draft, err := s.readyDraftToRead(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not readable")
		return nil, err
	}
	budget := scanBudget{pairs: s.setting.DiagnosisMaxPairs, timeout: s.setting.DiagnosisTimeout,
		enough: s.setting.DiagnosisHitRate}
	if budget.pairs <= 0 {
		budget.pairs = diagnosisPairsDefault
	}
	if budget.timeout <= 0 {
		budget.timeout = diagnosisTimeoutDefault
	}
	if budget.enough <= 0 {
		budget.enough = 0.6
	}
	timeout := min(s.setting.QueryTimeout, int(budget.timeout.Seconds()))
	if timeout <= 0 {
		timeout = int(budget.timeout.Seconds())
	}
	c := &vegaCounter{va: s.va, account: caller, timeoutSeconds: timeout}
	facts := networkFacts(ctx, draft, c, budget)
	span.SetStatus(codes.Ok, "")
	return facts, nil
}

// scanBudget bounds the inclusion scan: the field pairs counted and the time all counts take.
type scanBudget struct {
	pairs   int
	timeout time.Duration
	// enough is the rate at which a polymorphic branch's target is taken without counting the others.
	enough float64
}

// networkFacts computes the facts of the draft's network, counting with c within budget.
func networkFacts(ctx context.Context, draft *interfaces.Draft, c networkCounter,
	budget scanBudget) *interfaces.DraftNetworkFacts {
	items := &draft.Items
	facts := &interfaces.DraftNetworkFacts{Draft: draft, Components: NetworkComponents(items),
		Pending: pendingObjects(items), References: []interfaces.DraftReference{}, Subtypes: []interfaces.DraftSubtype{}}
	scan := &inclusionScan{items: items, c: c, budget: budget, language: draftLanguage(draft), fields: map[string][2]int64{}}
	countCtx, cancel := context.WithTimeout(ctx, budget.timeout)
	defer cancel()
	scan.run(countCtx, facts)
	return facts
}

// NetworkComponents are the adopted object types joined by adopted relation types, the largest
// first, each in the draft's order; components of one size keep the order of their first member.
// Where a fact was read is no business link: the source documents and blocks every document
// object cites are left out, or a line no relation ties to its record would count as connected.
func NetworkComponents(items *interfaces.DraftItems) [][]string {
	parent := map[string]string{}
	var find func(string) string
	find = func(id string) string {
		if parent[id] != id {
			parent[id] = find(parent[id])
		}
		return parent[id]
	}
	order := []string{}
	for _, ot := range items.ObjectTypes {
		if ot.Adopted && (ot.Document == nil || ot.Document.Layer != interfaces.DRAFT_DOCUMENT_LAYER_EVIDENCE) {
			parent[ot.ID] = ot.ID
			order = append(order, ot.ID)
		}
	}
	for _, rt := range items.RelationTypes {
		_, source := parent[rt.Source]
		_, target := parent[rt.Target]
		if rt.Adopted && source && target {
			parent[find(rt.Source)] = find(rt.Target)
		}
	}
	index := map[string]int{}
	components := [][]string{}
	for _, id := range order {
		root := find(id)
		i, ok := index[root]
		if !ok {
			i = len(components)
			index[root] = i
			components = append(components, []string{})
		}
		components[i] = append(components[i], id)
	}
	slices.SortStableFunc(components, func(a, b []string) int { return cmp.Compare(len(b), len(a)) })
	return components
}

// pendingObjects are the object types no one adopted, with the relations their adoption brings in.
func pendingObjects(items *interfaces.DraftItems) []interfaces.DraftPendingObject {
	adopted := map[string]bool{}
	for _, ot := range items.ObjectTypes {
		adopted[ot.ID] = ot.Adopted
	}
	pending := []interfaces.DraftPendingObject{}
	for _, ot := range items.ObjectTypes {
		if ot.Adopted || ot.Existing {
			continue
		}
		p := interfaces.DraftPendingObject{ObjectTypeID: ot.ID, NeedsIdentity: ot.NeedsIdentity, Relations: []string{}}
		if ot.NeedsIdentity {
			p.Candidates = ot.Key.Candidates
			if len(p.Candidates) == 0 && len(ot.Key.Candidate) > 0 {
				p.Candidates = [][]string{ot.Key.Candidate}
			}
		}
		for _, rt := range items.RelationTypes {
			other := ""
			switch ot.ID {
			case rt.Source:
				other = rt.Target
			case rt.Target:
				other = rt.Source
			default:
				continue
			}
			if !rt.Adopted && (adopted[other] || other == ot.ID) {
				p.Relations = append(p.Relations, rt.ID)
			}
		}
		pending = append(pending, p)
	}
	return pending
}

// scanPair is one field pair the scan may count.
type scanPair struct {
	ref      interfaces.DraftReference
	field    string
	key      string
	priority int
	// typeField and typeValue restrict the source records to one branch of a polymorphic reference.
	typeField string
	// known is the count a relation of the draft with this mapping already has.
	known *interfaces.DraftJoinEvidence
	// named is a pair whose field's name says its target.
	named bool
}

type inclusionScan struct {
	items    *interfaces.DraftItems
	c        networkCounter
	budget   scanBudget
	language string
	counted  int
	// fields are the rows of each source field counted over all its records and those left empty,
	// by source|property.
	fields map[string][2]int64
}

// room tells whether the budget lets one more count run.
func (s *inclusionScan) room(ctx context.Context) bool {
	return s.counted < s.budget.pairs && ctx.Err() == nil
}

// identityOf is an object type's single identity property, when it has exactly one.
func identityOf(ot *interfaces.DraftObjectType) *interfaces.DraftProperty {
	if ot.NeedsIdentity || len(ot.PrimaryKeys) != 1 || ot.Table.ResourceID == "" {
		return nil
	}
	return findProperty(ot, ot.PrimaryKeys[0])
}

// targetKeys are the properties a reference to the object type may point at: its single identity, or,
// while it waits for one, a single-field candidate drafting counted unique; and the fields holding a
// document's number, which rows of one document share.
func targetKeys(ot *interfaces.DraftObjectType) []*interfaces.DraftProperty {
	if ot.Table.ResourceID == "" {
		return nil
	}
	keys := []*interfaces.DraftProperty{}
	if key := identityOf(ot); key != nil {
		keys = append(keys, key)
	} else if ot.NeedsIdentity {
		for _, candidate := range ot.Key.Candidates {
			if p := findProperty(ot, candidate[0]); len(candidate) == 1 && p != nil {
				keys = append(keys, p)
				break
			}
		}
	}
	for _, p := range ot.Properties {
		if p.MappedField != "" && typeClass(p.Type) == "string" && documentNumber(p) && !slices.Contains(keys, p) {
			keys = append(keys, p)
		}
	}
	return keys
}

// documentNumber tells a field holding a document's own number; a source document's number (源单编号)
// is a reference, not a key.
func documentNumber(p *interfaces.DraftProperty) bool {
	if sourceLike(p) {
		return false
	}
	for _, n := range namesOf(p) {
		key := nameKey(n)
		for _, end := range locale.DiagnosisDocumentNumberEnds {
			if key != "" && strings.HasSuffix(key, end) {
				return true
			}
		}
	}
	return false
}

// sequenceLike tells a field numbering rows, never a reference.
func sequenceLike(p *interfaces.DraftProperty) bool {
	for _, n := range namesOf(p) {
		key := nameKey(n)
		if key == "" {
			continue
		}
		for _, w := range locale.DiagnosisSequenceWords {
			if w[0] >= 0x80 && strings.Contains(key, w) || w[0] < 0x80 && (key == w || strings.HasSuffix(key, w)) {
				return true
			}
		}
	}
	return false
}

// sourceLike tells a field naming where a record comes from.
func sourceLike(p *interfaces.DraftProperty) bool {
	return slices.ContainsFunc(namesOf(p), func(n string) bool {
		if n == "" {
			return false
		}
		lower := strings.ToLower(n)
		for _, w := range locale.DiagnosisSourceWords {
			if w[0] >= 0x80 && strings.Contains(n, w) || w[0] < 0x80 && (slices.Contains(words(n), w) ||
				strings.HasPrefix(nameKey(lower), w)) {
				return true
			}
		}
		return false
	})
}

func (s *inclusionScan) run(ctx context.Context, facts *interfaces.DraftNetworkFacts) {
	types := []*interfaces.DraftObjectType{}
	for _, ot := range s.items.ObjectTypes {
		if ot.Table.ResourceID != "" && ot.Document == nil {
			types = append(types, ot)
		}
	}
	// The order spends the budget on what matters most: subtypes, few and decisive; then the branches
	// of polymorphic references; then plain fields, those naming where a record comes from first, then
	// those whose name says the target, then the rest.
	subtypes, pairsSeen, notCounted := s.subtypes(ctx, types)
	facts.Subtypes = subtypes
	facts.Pairs, facts.NotCounted = pairsSeen, notCounted
	branches := s.polymorphic(ctx, types)

	pairs := []scanPair{}
	for _, source := range types {
		inKey := map[string]bool{}
		for _, k := range source.PrimaryKeys {
			inKey[k] = true
		}
		for _, p := range source.Properties {
			if inKey[p.Name] || p.MappedField == "" || !codeLike(p) || sequenceLike(p) {
				continue
			}
			for _, target := range types {
				for _, key := range targetKeys(target) {
					if pair, ok := s.pairOf(source, p, target, key); ok {
						pairs = append(pairs, pair)
					}
				}
			}
		}
	}
	slices.SortStableFunc(pairs, func(a, b scanPair) int { return cmp.Compare(b.priority, a.priority) })
	plain := s.count(ctx, pairs)
	facts.Pairs += len(pairs) + branches.pairs
	facts.NotCounted += plain.notCounted + branches.notCounted
	// A branch found replaces the plain count of its field and target: the branch is the reference.
	branched := map[string]bool{}
	for _, b := range branches.found {
		branched[b.Source+"|"+b.SourceProperty+"|"+b.Target] = true
	}
	for _, r := range plain.found {
		if !branched[r.Source+"|"+r.SourceProperty+"|"+r.Target] {
			facts.References = append(facts.References, r)
		}
	}
	facts.References = append(facts.References, branches.found...)
	for i := range facts.References {
		r := &facts.References[i]
		if f, ok := s.fields[r.Source+"|"+r.SourceProperty]; ok {
			r.FieldRows, r.FieldNulls = f[0], f[1]
		}
	}
	facts.Uncounted = []string{}
	for _, id := range append(plain.uncounted, branches.uncounted...) {
		if !slices.Contains(facts.Uncounted, id) {
			facts.Uncounted = append(facts.Uncounted, id)
		}
	}
}

// pairOf is a code-like property of source counted against one of target's keys: of another object
// type, of a matching type, or of the source itself through a parent field.
func (s *inclusionScan) pairOf(source *interfaces.DraftObjectType, p *interfaces.DraftProperty,
	target *interfaces.DraftObjectType, key *interfaces.DraftProperty) (scanPair, bool) {
	switch {
	case key == nil, source.Table.CatalogID != target.Table.CatalogID, typeClass(p.Type) != typeClass(key.Type):
		return scanPair{}, false
	case source.ID == target.ID && !parentLike(p), source.ID == target.ID && key == p:
		return scanPair{}, false
	}
	priority := namePriority(p, target, key)
	pair := scanPair{field: p.MappedField, key: key.MappedField, named: priority > 0, priority: priority,
		ref: interfaces.DraftReference{Source: source.ID, SourceProperty: p.Name, Target: target.ID,
			TargetProperty: key.Name, Self: source.ID == target.ID, Name: s.nameOf(source, target, p.Name)}}
	for _, rt := range s.items.RelationTypes {
		if rt.LinkTable == nil && rt.Source == source.ID && rt.Target == target.ID && len(rt.Mapping) == 1 &&
			rt.Mapping[0] == (interfaces.DraftFieldMapping{SourceProperty: p.Name, TargetProperty: key.Name}) {
			pair.ref.RelationTypeID = rt.ID
			if rt.Evidence.Computed {
				evidence := rt.Evidence
				pair.known = &evidence
			}
			pair.priority = 10
		}
	}
	if sourceLike(p) && pair.priority < 10 {
		// A source field refers to a document: its number first, then other identities.
		pair.priority += 4
		if documentNumber(key) {
			pair.priority += 2
		}
	}
	return pair, true
}

// nameOf is the name a relation drafted from the field takes: the role the target plays. A role that
// merely extends the target's name, as 物料计划 from 物料计划编号 for 物料, names no narrower role: the
// target's name does.
func (s *inclusionScan) nameOf(source *interfaces.DraftObjectType, target *interfaces.DraftObjectType, property string) string {
	name := relationNameIn(s.language, source, target, property, target.Name)
	if target.Name != "" && name != target.Name && strings.HasPrefix(name, target.Name) {
		name = target.Name
	}
	if relationNameTaken(s.items.RelationTypes, source.ID, "", name) {
		name = relationNameFullIn(source, target, property, target.Name)
	}
	return name
}

// scanFound are the references a count found, with how many pairs it left uncounted.
type scanFound struct {
	found      []interfaces.DraftReference
	notCounted int
	pairs      int
	// uncounted are the object types at either end of a pair left uncounted.
	uncounted []string
}

// count counts pairs in order within the budget. A pair counted without a hit is no reference; one
// left uncounted is kept when its field's name says its target, so a person can still see it.
func (s *inclusionScan) count(ctx context.Context, pairs []scanPair) scanFound {
	out := scanFound{found: []interfaces.DraftReference{}}
	for _, pair := range pairs {
		r := pair.ref
		switch {
		case pair.known != nil:
			r.Counted, r.Rows, r.Nulls, r.Hits, r.Rate = true, pair.known.Rows, pair.known.Nulls, pair.known.Hits, pair.known.HitRate
		case !s.room(ctx):
			out.notCounted++
			out.uncounted = append(out.uncounted, r.Source, r.Target)
			if pair.named {
				out.found = append(out.found, r)
			}
			continue
		default:
			s.counted++
			source := &interfaces.VegaTable{ID: findObjectType(s.items, r.Source).Table.ResourceID}
			target := &interfaces.VegaTable{ID: findObjectType(s.items, r.Target).Table.ResourceID}
			var rows, nulls, orphans int64
			var err error
			if pair.typeField == "" {
				rows, nulls, orphans, _, err = s.c.joinStats(ctx, source, pair.field, target, pair.key)
			} else {
				rows, nulls, orphans, _, err = s.c.joinStatsWhere(ctx, source, pair.field, target, pair.key,
					pair.typeField, r.Condition.Value)
			}
			if err != nil {
				out.notCounted++
				out.uncounted = append(out.uncounted, r.Source, r.Target)
				if pair.named {
					out.found = append(out.found, r)
				}
				continue
			}
			e := joinEvidence(rows, nulls, orphans)
			r.Counted, r.Rows, r.Nulls, r.Hits, r.Rate = true, e.Rows, e.Nulls, e.Hits, e.HitRate
		}
		if r.Condition == nil && r.Counted {
			s.fields[r.Source+"|"+r.SourceProperty] = [2]int64{r.Rows, r.Nulls}
		}
		// Integers coincide: between integer fields the name says the target, or nearly all values hit.
		if r.RelationTypeID == "" && !pair.named && typeClass(findProperty(findObjectType(s.items, r.Source),
			r.SourceProperty).Type) == "integer" && r.Rate < diagnosisIntegerRate {
			continue
		}
		if r.Hits > 0 || r.RelationTypeID != "" {
			out.found = append(out.found, r)
		}
	}
	return out
}

// polymorphic counts each type field and the code field beside it, per type value, against every
// identity the code field could refer to; the best target of each value is its branch.
func (s *inclusionScan) polymorphic(ctx context.Context, types []*interfaces.DraftObjectType) scanFound {
	out := scanFound{found: []interfaces.DraftReference{}}
	for _, source := range types {
		for _, tf := range source.Properties {
			if tf.MappedField == "" || typeClass(tf.Type) != "string" || !typeLike(tf) {
				continue
			}
			for _, p := range source.Properties {
				if p == tf || p.MappedField == "" || !codeLike(p) || !sameStem(tf, p) {
					continue
				}
				if !s.room(ctx) {
					// The branches stay unknown: one pair, not counted.
					out.pairs++
					out.notCounted++
					out.uncounted = append(out.uncounted, source.ID)
					continue
				}
				s.counted++
				values, err := s.c.typeValues(ctx, &interfaces.VegaTable{ID: source.Table.ResourceID}, tf.MappedField,
					diagnosisTypeValues)
				if err != nil {
					continue
				}
				for _, value := range values {
					pairs := []scanPair{}
					for _, target := range types {
						if target.ID == source.ID {
							continue
						}
						for _, key := range targetKeys(target) {
							pair, ok := s.pairOf(source, p, target, key)
							if !ok {
								continue
							}
							pair.known, pair.ref.RelationTypeID = nil, ""
							pair.typeField = tf.MappedField
							pair.ref.Condition = &interfaces.DraftReferenceCondition{Property: tf.Name, Value: value}
							pair.priority += valuePriority(value, target)
							pair.named = pair.named || valuePriority(value, target) > 0
							pairs = append(pairs, pair)
						}
					}
					slices.SortStableFunc(pairs, func(a, b scanPair) int { return cmp.Compare(b.priority, a.priority) })
					// The branch is the first target, in that order, whose count reaches the rate; else the
					// one the most values hit. Past it the targets are not counted.
					var best *interfaces.DraftReference
					for i, pair := range pairs {
						counted := s.count(ctx, []scanPair{pair})
						out.pairs++
						out.notCounted += counted.notCounted
						out.uncounted = append(out.uncounted, counted.uncounted...)
						for _, r := range counted.found {
							if r.Counted && (best == nil || r.Rate > best.Rate) {
								best = &r
							}
						}
						if best != nil && best.Rate >= s.budget.enough {
							out.pairs += len(pairs) - i - 1
							break
						}
					}
					if best != nil {
						out.found = append(out.found, *best)
					}
				}
			}
		}
	}
	return out
}

// subtypes counts the identities of object types whose identity fields are named alike: one whose
// every value is a value of the other's, and not the other way round, is a kind of it.
func (s *inclusionScan) subtypes(ctx context.Context, types []*interfaces.DraftObjectType) ([]interfaces.DraftSubtype, int, int) {
	found, pairs, notCounted := []interfaces.DraftSubtype{}, 0, 0
	contained := func(a, b *interfaces.DraftObjectType, ka, kb *interfaces.DraftProperty) (int64, int64, bool) {
		s.counted++
		rows, nulls, orphans, _, err := s.c.joinStats(ctx, &interfaces.VegaTable{ID: a.Table.ResourceID}, ka.MappedField,
			&interfaces.VegaTable{ID: b.Table.ResourceID}, kb.MappedField)
		if err != nil {
			return 0, 0, false
		}
		e := joinEvidence(rows, nulls, orphans)
		return e.Rows, e.Hits, e.Hits > 0 && e.Orphans == 0
	}
	for i, a := range types {
		for _, b := range types[i+1:] {
			ka, kb := identityOf(a), identityOf(b)
			if ka == nil || kb == nil || a.Table.ResourceID == b.Table.ResourceID || a.Table.CatalogID != b.Table.CatalogID ||
				typeClass(ka.Type) != typeClass(kb.Type) || !sameName(ka, kb) {
				continue
			}
			pairs++
			if !s.room(ctx) {
				notCounted++
				continue
			}
			rowsAB, hitsAB, aInB := contained(a, b, ka, kb)
			if !s.room(ctx) {
				notCounted++
				continue
			}
			rowsBA, hitsBA, bInA := contained(b, a, kb, ka)
			switch {
			case aInB && !bInA:
				found = append(found, interfaces.DraftSubtype{Sub: a.ID, Super: b.ID, SubProperty: ka.Name,
					SuperProperty: kb.Name, Rows: rowsAB, Hits: hitsAB})
			case bInA && !aInB:
				found = append(found, interfaces.DraftSubtype{Sub: b.ID, Super: a.ID, SubProperty: kb.Name,
					SuperProperty: ka.Name, Rows: rowsBA, Hits: hitsBA})
			}
		}
	}
	return found, pairs, notCounted
}

// typeClass groups property types that can hold the same codes.
func typeClass(t string) string {
	switch t {
	case "integer", "unsigned integer":
		return "integer"
	case "string", "text":
		return "string"
	}
	return ""
}

// nameKey is a name in lower case without spaces, underscores, hyphens and dots.
func nameKey(name string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '_' || r == '-' || r == '.' {
			return -1
		}
		return unicode.ToLower(r)
	}, name)
}

// namesOf are the names a property is known by: its display name and its field.
func namesOf(p *interfaces.DraftProperty) []string {
	return []string{p.DisplayName, p.MappedField, p.Name}
}

// hasWord tells whether a name holds one of words: a Latin word as a whole word or as the end of
// the name, any other anywhere in it.
func hasWord(name string, list []string) bool {
	lower := nameKey(name)
	ws := words(name)
	for _, w := range list {
		if w[0] < 0x80 {
			if slices.Contains(ws, w) || strings.HasSuffix(lower, w) && len(lower) > len(w) {
				return true
			}
		} else if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// codeLike tells a string or integer property whose name says it holds a code.
func codeLike(p *interfaces.DraftProperty) bool {
	if typeClass(p.Type) == "" {
		return false
	}
	return slices.ContainsFunc(namesOf(p), func(n string) bool { return n != "" && hasWord(n, locale.DiagnosisCodeWords) })
}

func typeLike(p *interfaces.DraftProperty) bool {
	return slices.ContainsFunc(namesOf(p), func(n string) bool { return n != "" && hasWord(n, locale.DiagnosisTypeWords) })
}

func parentLike(p *interfaces.DraftProperty) bool {
	return slices.ContainsFunc(namesOf(p), func(n string) bool { return n != "" && hasWord(n, locale.DiagnosisParentWords) })
}

// stemOf is a name without the type or code word it ends with.
func stemOf(name string, list []string) string {
	lower := nameKey(name)
	for _, w := range list {
		if strings.HasSuffix(lower, w) && len(lower) > len(w) {
			return lower[:len(lower)-len(w)]
		}
	}
	return ""
}

// sameStem tells a type field and a code field of one reference: their names are the same without
// the type word and the code word, as 源单类型 and 源单编号, source_type and source_no.
func sameStem(typeField *interfaces.DraftProperty, code *interfaces.DraftProperty) bool {
	for _, pair := range [][2]string{{typeField.DisplayName, code.DisplayName}, {typeField.MappedField, code.MappedField}} {
		a, b := stemOf(pair[0], locale.DiagnosisTypeWords), stemOf(pair[1], locale.DiagnosisCodeWords)
		if a != "" && a == b {
			return true
		}
	}
	return false
}

// sameName tells two identity properties named alike: by display name when both have one, as 物料编码
// and 物料编码, else by field. Generic fields such as id named apart by their display names are not.
func sameName(a, b *interfaces.DraftProperty) bool {
	if a.DisplayName != "" && b.DisplayName != "" {
		return nameKey(a.DisplayName) == nameKey(b.DisplayName)
	}
	return nameKey(a.MappedField) == nameKey(b.MappedField)
}

// namePriority ranks a pair by how far the field's name says its target: it names the target or its
// identity, or its stem is the target's.
func namePriority(p *interfaces.DraftProperty, target *interfaces.DraftObjectType, key *interfaces.DraftProperty) int {
	priority := 0
	for _, n := range namesOf(p) {
		n = nameKey(n)
		if n == "" {
			continue
		}
		for _, t := range []string{target.Name, key.DisplayName, key.MappedField} {
			if t = nameKey(t); t == "" {
				continue
			}
			switch {
			case n == t:
				priority = max(priority, 3)
			case meaningful(t) && strings.Contains(n, t) || meaningful(n) && strings.Contains(t, n):
				priority = max(priority, 2)
			}
		}
		if stem := stemOf(n, locale.DiagnosisCodeWords); stem != "" && stem == stemOf(nameKey(key.MappedField), locale.DiagnosisCodeWords) {
			priority = max(priority, 1)
		}
	}
	return priority
}

// meaningful tells a name long enough to say something when another contains it: not a code word
// alone, such as id, and at least two Han characters or four letters.
func meaningful(key string) bool {
	if slices.Contains(locale.DiagnosisCodeWords, key) {
		return false
	}
	if key[0] >= 0x80 {
		return utf8.RuneCountInString(key) >= 2
	}
	return len(key) >= 4
}

// valuePriority ranks a branch's target by how far the type value names it, as mrp_planorder names a
// table erp_mrp_plan_order.
func valuePriority(value string, target *interfaces.DraftObjectType) int {
	v := nameKey(value)
	if v == "" {
		return 0
	}
	for _, n := range []string{target.Table.SourceName, target.Table.Name, target.ID, target.Name} {
		if n = nameKey(n); n != "" && (strings.Contains(n, v) || strings.Contains(v, n)) {
			return 5
		}
	}
	return 0
}

func (s *draftService) DismissSuggestion(ctx context.Context, token string, projectID string, suggestionID string,
	reason string, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DismissSuggestion")
	defer span.End()

	if _, err := s.unarchivedProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, err := s.readyDraftToRead(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not readable")
		return nil, err
	}
	decision := &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_DIAGNOSIS, ItemID: suggestionID,
		Action: interfaces.DRAFT_DECISION_DISMISS, After: true, Reason: truncateRunes(strings.TrimSpace(reason),
			interfaces.DRAFT_REASON_MAX_LENGTH)}
	if err := s.commit(ctx, draft, []*interfaces.DraftDecision{decision}, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}
