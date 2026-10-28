// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package merge is the semantic three-way merge of two bkn/ definition sets
// onto their merge base. Definitions are matched by kind and id and merged
// field by field, so a conflict is only a change to the same field of the same
// definition on both sides. Files that are not BKN definitions are merged
// whole, by path.
package merge

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Definition kinds, as the SDK names them in a network diff.
const (
	kindNetwork      = "network"
	kindObjectType   = "object_type"
	kindRelationType = "relation_type"
	kindActionType   = "action_type"
	kindRiskType     = "risk_type"
	kindConceptGroup = "concept_group"
	kindValueType    = "value_type"
)

// kindDirs maps the directory of a definition file to its kind.
var kindDirs = map[string]string{
	"object_types":   kindObjectType,
	"relation_types": kindRelationType,
	"action_types":   kindActionType,
	"risk_types":     kindRiskType,
	"concept_groups": kindConceptGroup,
	"value_types":    kindValueType,
}

// ignoredFields are derived from other fields or from the file text, so they take no part in the
// comparison: RawContent is the whole file, Summary is cut from Description and the Has* flags
// record which sections the parser saw. The SDK's own diff ignores the same fields.
var ignoredFields = []string{"RawContent", "SkillContent", "Summary", "HasDataPropertiesSection", "HasKeysSection"}

// elementKeyFields are tried in order to key an array of objects, the same fields the SDK's diff
// aligns a table on, so a property is merged by its name rather than by its row.
var elementKeyFields = []string{"Name", "Property", "SourceProperty", "Field", "ID"}

type defKey struct {
	kind string
	id   string
}

// definition is one definition, or one opaque file, of a side.
type definition struct {
	path string
	data []byte
	// model is the SDK struct; nil for an opaque file.
	model any
	// norm is the model as generic JSON without derived fields; the bytes as a string for an
	// opaque file.
	norm any
}

// Merge merges ours and theirs onto base. Each set maps the paths inside bkn/
// to file contents, without CHECKSUM. Definitions that come out unchanged from
// one side keep that side's bytes; only definitions combined field by field are
// serialized again. Conflicts do not fail the merge: the conflicting parts keep
// the ours side and are listed in the result.
func Merge(base, ours, theirs map[string][]byte) (*interfaces.MergeResult, error) {
	b, err := loadSide(base)
	if err != nil {
		return nil, fmt.Errorf("load base: %w", err)
	}
	o, err := loadSide(ours)
	if err != nil {
		return nil, fmt.Errorf("load ours: %w", err)
	}
	t, err := loadSide(theirs)
	if err != nil {
		return nil, fmt.Errorf("load theirs: %w", err)
	}

	m := &merger{
		result:   &interfaces.MergeResult{Files: map[string][]byte{}, Conflicts: []interfaces.MergeConflict{}},
		models:   map[defKey]any{},
		outcome:  map[defKey]*int{},
		owners:   map[string]owner{},
		collided: map[defKey]bool{},
	}
	for _, k := range unionKeys(b, o, t) {
		if err := m.mergeDefinition(k, b[k], o[k], t[k]); err != nil {
			return nil, err
		}
	}
	if err := m.serializeMerged(ours); err != nil {
		return nil, err
	}
	if err := m.checkReferences(ours, theirs); err != nil {
		return nil, err
	}
	return m.result, nil
}

// checkReferences validates the merged set as a whole. Each side may be valid on its own while
// the merge is not, for example a relation added by one side to an object type the other side
// deleted. Validation errors that neither side already had are reported as conflicts; a merged
// set that does not load at all is an error.
func (m *merger) checkReferences(ours, theirs map[string][]byte) error {
	merged, err := validationErrors(m.result.Files)
	if err != nil {
		return fmt.Errorf("load merged definitions: %w", err)
	}
	known := map[string]bool{}
	for _, side := range []map[string][]byte{ours, theirs} {
		errs, err := validationErrors(side)
		if err != nil {
			return err
		}
		for key := range errs {
			known[key] = true
		}
	}

	files := map[defKey]string{}
	for p, data := range m.result.Files {
		if k, ok, _ := definitionKey(p, data); ok {
			files[k] = p
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		if !known[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		ve := merged[key]
		c := interfaces.MergeConflict{
			Reason: interfaces.MERGE_CONFLICT_BROKEN_REFERENCE,
			Path:   snakeCase(ve.Column),
			Detail: ve.Message,
		}
		var k defKey
		if ve.Table == bkn.RootFileName {
			// Errors across the whole network, such as duplicate names, sit on network.bkn.
			k = defKey{kind: kindNetwork}
			c.Type, c.File = kindNetwork, bkn.RootFileName
			if net, ok := m.models[k].(*bkn.BknNetwork); ok {
				c.ID = net.ID
			}
		} else {
			kind, id, _ := strings.Cut(ve.Table, ":")
			k = defKey{kind, id}
			c.Type, c.ID, c.File = kind, id, files[k]
		}
		m.result.Conflicts = append(m.result.Conflicts, c)
		m.count(k, &m.result.Summary.Conflicted)
	}
	return nil
}

// validationErrors runs the SDK's network validation, and for a 3.0 package its declarations that
// span definitions, over a file set, keyed so that the same error on two sets compares equal.
func validationErrors(files map[string][]byte) (map[string]bkn.ValidationError, error) {
	out := map[string]bkn.ValidationError{}
	if len(files) == 0 {
		return out, nil
	}
	net, err := loadNetwork(files)
	if err != nil {
		return nil, err
	}
	errs := append(bkn.ValidateNetwork(net).Errors, declarationErrors(net)...)
	for _, ve := range errs {
		out[strings.Join([]string{ve.Table, ve.Column, ve.Code, ve.Message}, "\x00")] = ve
	}
	return out, nil
}

// IntroducedErrors lists the validation errors of after that before does not have: the
// specification's checks and the 3.0 declarations that span definitions, such as two object types
// of one name, which the loader alone lets through. A set's own older errors are not the writer's.
func IntroducedErrors(before map[string][]byte, after map[string][]byte) ([]bkn.ValidationError, error) {
	had, err := validationErrors(before)
	if err != nil {
		return nil, err
	}
	has, err := validationErrors(after)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(has))
	for key := range has {
		if _, ok := had[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	introduced := make([]bkn.ValidationError, 0, len(keys))
	for _, key := range keys {
		introduced = append(introduced, has[key])
	}
	return introduced, nil
}

// loadNetwork loads a file set with the SDK loader. The 3.0 declarations that span definitions are
// left to declarationErrors: a merge of two valid sides can break them, which is a conflict to
// report, not a set that fails to load.
func loadNetwork(files map[string][]byte) (*bkn.BknNetwork, error) {
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	return bkn.LoadNetworkDefinitionsWithFS(fsys, ".")
}

// declarationCode marks a 3.0 declaration error among the validation errors.
const declarationCode = "declaration"

// declarationErrors lists the 3.0 declarations of net that span definitions and do not hold, each
// on the definition it belongs to. NetworkRelationIssues lists the lifecycle and relation ones with
// the definitions involved. ValidateNetworkDeclarations stops at its first error and names the
// definition only in its message, so the value type references and the aliases are checked one
// definition at a time, on a network that holds that definition with every value type and the bare
// names of its peers. A value type name used twice is put on network.bkn; so is the error of the
// full check when none of these finds one, so that no refusal goes unreported.
func declarationErrors(net *bkn.BknNetwork) []bkn.ValidationError {
	if net.BknSpec != bkn.BknSpecV3 {
		return nil
	}
	var out []bkn.ValidationError
	add := func(table string, err error) {
		if err != nil {
			out = append(out, bkn.ValidationError{Table: table, Code: declarationCode, Message: err.Error()})
		}
	}
	for _, issue := range bkn.NetworkRelationIssues(net) {
		add(issue.Involved[0].Kind+":"+issue.Involved[0].ID, issue.Err)
	}
	add(bkn.RootFileName, bkn.ValidateNetworkDeclarations(&bkn.BknNetwork{ValueTypes: net.ValueTypes}))

	// The probes keep of a value type only what a reference is checked against; the id stands in for
	// the name, so a name used twice is reported once, above.
	valueTypes := make([]*bkn.BknValueType, 0, len(net.ValueTypes))
	for _, vt := range net.ValueTypes {
		valueTypes = append(valueTypes, &bkn.BknValueType{BknValueTypeFrontmatter: bkn.BknValueTypeFrontmatter{ID: vt.ID, Name: vt.ID}, BaseType: vt.BaseType})
	}
	for _, ot := range net.ObjectTypes {
		probe := &bkn.BknNetwork{ValueTypes: valueTypes}
		for _, other := range net.ObjectTypes {
			bare := &bkn.BknObjectType{BknObjectTypeFrontmatter: bkn.BknObjectTypeFrontmatter{ID: other.ID, Name: other.Name}}
			if other == ot {
				bare.Aliases, bare.DataProperties = ot.Aliases, ot.DataProperties
			}
			probe.ObjectTypes = append(probe.ObjectTypes, bare)
		}
		add(kindObjectType+":"+ot.ID, bkn.ValidateNetworkDeclarations(probe))
	}
	for _, rt := range net.RelationTypes {
		probe := &bkn.BknNetwork{ValueTypes: valueTypes, RelationTypes: []*bkn.BknRelationType{{
			BknRelationTypeFrontmatter: bkn.BknRelationTypeFrontmatter{ID: rt.ID}, DataProperties: rt.DataProperties}}}
		add(kindRelationType+":"+rt.ID, bkn.ValidateNetworkDeclarations(probe))
	}
	for _, at := range net.ActionTypes {
		probe := &bkn.BknNetwork{}
		for _, other := range net.ActionTypes {
			bare := &bkn.BknActionType{BknActionTypeFrontmatter: bkn.BknActionTypeFrontmatter{ID: other.ID, Name: other.Name}}
			if other == at {
				bare.Aliases = at.Aliases
			}
			probe.ActionTypes = append(probe.ActionTypes, bare)
		}
		add(kindActionType+":"+at.ID, bkn.ValidateNetworkDeclarations(probe))
	}
	if len(out) == 0 {
		add(bkn.RootFileName, bkn.ValidateNetworkDeclarations(net))
	}
	return out
}

type merger struct {
	result *interfaces.MergeResult
	// models holds the SDK model of every definition in the result.
	models map[defKey]any
	// rewritten lists the definitions that were combined and must be serialized again.
	rewritten []rewrite
	// outcome is the summary counter each definition was counted under, so a later conflict
	// moves it rather than counting it twice.
	outcome map[defKey]*int
	// owners maps each result path to the definition written there; collided marks a
	// definition that lost its path to another one.
	owners   map[string]owner
	collided map[defKey]bool
}

// count records the outcome of definition k once. A definition that lost its path to another
// one is counted as conflicted whatever the caller decided.
func (m *merger) count(k defKey, counter *int) {
	if m.collided[k] {
		counter = &m.result.Summary.Conflicted
	}
	if prev, ok := m.outcome[k]; ok {
		if prev == counter {
			return
		}
		*prev--
	}
	*counter++
	m.outcome[k] = counter
}

// owner is the definition written at a result path and the side it came from.
type owner struct {
	key  defKey
	def  *definition
	ours bool
}

// claim reserves path for definition k of one side. Two definitions can land on one path, e.g.
// when both sides change the id inside the same file. One side never has two files at one path,
// so the two come from different sides, and the path keeps ours whichever arrives first: the
// theirs definition is refused, or evicted if it came first, and recorded as a conflict.
func (m *merger) claim(k defKey, d *definition, ours bool) bool {
	prev, taken := m.owners[d.path]
	if !taken || prev.key == k {
		m.owners[d.path] = owner{k, d, ours}
		return true
	}
	if !ours || prev.ours {
		m.collide(k, d)
		return false
	}
	delete(m.models, prev.key)
	for i, r := range m.rewritten {
		if r.key == prev.key {
			m.rewritten = append(m.rewritten[:i], m.rewritten[i+1:]...)
			break
		}
	}
	m.collide(prev.key, prev.def)
	m.count(prev.key, &m.result.Summary.Conflicted)
	m.owners[d.path] = owner{k, d, ours}
	return true
}

// collide records that definition k lost its path to another definition.
func (m *merger) collide(k defKey, d *definition) {
	m.conflict(k, d, interfaces.MERGE_CONFLICT_BOTH_CHANGED, nil)
	m.collided[k] = true
}

type rewrite struct {
	key  defKey
	path string
}

func (m *merger) take(k defKey, d *definition, ours bool) {
	if !m.claim(k, d, ours) {
		return
	}
	m.result.Files[d.path] = d.data
	if d.model != nil {
		m.models[k] = d.model
	}
}

func (m *merger) mergeDefinition(k defKey, base, ours, theirs *definition) error {
	sum := &m.result.Summary
	switch {
	case ours == nil && theirs == nil:
		m.count(k, &sum.Deleted)
	case base == nil && theirs == nil:
		m.take(k, ours, true)
		m.count(k, &sum.Ours)
	case base == nil && ours == nil:
		m.take(k, theirs, false)
		m.count(k, &sum.Theirs)
	case ours == nil:
		if equal(theirs.norm, base.norm) {
			m.count(k, &sum.Deleted)
			return nil
		}
		m.conflict(k, theirs, interfaces.MERGE_CONFLICT_DELETED_BY_OURS, nil)
		m.count(k, &sum.Conflicted)
	case theirs == nil:
		if equal(ours.norm, base.norm) {
			m.count(k, &sum.Deleted)
			return nil
		}
		m.conflict(k, ours, interfaces.MERGE_CONFLICT_DELETED_BY_THEIRS, nil)
		m.take(k, ours, true)
		m.count(k, &sum.Conflicted)
	case equal(ours.norm, theirs.norm):
		m.take(k, ours, true)
		if base != nil && equal(ours.norm, base.norm) {
			m.count(k, &sum.Unchanged)
		} else {
			m.count(k, &sum.Ours)
		}
	case base != nil && equal(ours.norm, base.norm):
		m.take(k, theirs, false)
		m.count(k, &sum.Theirs)
	case base != nil && equal(theirs.norm, base.norm):
		m.take(k, ours, true)
		m.count(k, &sum.Ours)
	case k.kind == interfaces.MERGE_TYPE_FILE:
		m.conflict(k, ours, interfaces.MERGE_CONFLICT_BOTH_CHANGED, nil)
		m.take(k, ours, true)
		m.count(k, &sum.Conflicted)
	default:
		return m.mergeFields(k, base, ours, theirs)
	}
	return nil
}

// mergeFields combines a definition both sides changed, field by field. A definition added on
// both sides is merged onto an empty base.
func (m *merger) mergeFields(k defKey, base, ours, theirs *definition) error {
	var baseNorm any
	if base != nil {
		baseNorm = base.norm
	}
	var conflicts []fieldConflict
	merged := merge3("", baseNorm, ours.norm, theirs.norm, &conflicts)

	if !m.claim(k, ours, true) {
		m.count(k, &m.result.Summary.Conflicted)
		return nil
	}
	model, err := decodeModel(k.kind, merged)
	if err != nil {
		return fmt.Errorf("decode merged %s %s: %w", k.kind, k.id, err)
	}
	m.models[k] = model
	m.rewritten = append(m.rewritten, rewrite{key: k, path: ours.path})

	if len(conflicts) == 0 {
		m.count(k, &m.result.Summary.Merged)
		return nil
	}
	m.count(k, &m.result.Summary.Conflicted)
	for i := range conflicts {
		m.conflict(k, ours, interfaces.MERGE_CONFLICT_BOTH_CHANGED, &conflicts[i])
	}
	return nil
}

// conflict records a conflict on the definition d of one side.
func (m *merger) conflict(k defKey, d *definition, reason string, fc *fieldConflict) {
	c := interfaces.MergeConflict{Type: k.kind, ID: k.id, File: d.path, Reason: reason}
	if net, ok := d.model.(*bkn.BknNetwork); ok {
		c.ID = net.ID
	}
	if fc != nil {
		c.Path, c.Base, c.Ours, c.Theirs = fc.path, render(fc.base), render(fc.ours), render(fc.theirs)
	}
	m.result.Conflicts = append(m.result.Conflicts, c)
}

// serializeMerged writes the combined definitions with the SDK serializer, under the BKN
// specification of the merged network, and checks that each reads back as the merged model.
// A combined definition that breaks a rule of its own, though each side's change was valid,
// is a conflict and keeps ours.
func (m *merger) serializeMerged(ours map[string][]byte) error {
	if len(m.rewritten) == 0 {
		return nil
	}
	written, err := m.serialize()
	if err != nil {
		return err
	}
	if m.dropInvalid(ours, written) {
		if written, err = m.serialize(); err != nil {
			return err
		}
	}
	for _, r := range m.rewritten {
		data, ok := written[serializedPath(r.key)]
		if !ok {
			return fmt.Errorf("serializer wrote no file for %s %s", r.key.kind, r.key.id)
		}
		m.result.Files[r.path] = data
	}

	reloaded, err := loadSide(m.result.Files)
	if err != nil {
		return fmt.Errorf("load merged definitions: %w", err)
	}
	for _, r := range m.rewritten {
		got, ok := reloaded[r.key]
		if !ok {
			return fmt.Errorf("merged %s %s does not load back", r.key.kind, r.key.id)
		}
		want, err := normalize(m.models[r.key])
		if err != nil {
			return err
		}
		if !equal(got.norm, want) {
			return fmt.Errorf("merged %s %s does not read back as merged", r.key.kind, r.key.id)
		}
	}
	return nil
}

// serialize writes the merged network with the SDK serializer and returns its files by path.
func (m *merger) serialize() (map[string][]byte, error) {
	net, err := m.network()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := bkn.WriteNetworkToTar(net, &buf); err != nil {
		return nil, fmt.Errorf("serialize merged network: %w", err)
	}
	written, err := readTar(&buf)
	if err != nil {
		return nil, fmt.Errorf("read serialized network: %w", err)
	}
	return written, nil
}

// dropInvalid loads each combined definition in place of ours' file within ours, which loads,
// so a failure belongs to that definition. Such a definition is recorded as a conflict and goes
// back to ours. It reports whether any was dropped.
func (m *merger) dropInvalid(ours, written map[string][]byte) bool {
	kept := m.rewritten[:0]
	for _, r := range m.rewritten {
		probe := maps.Clone(ours)
		probe[r.path] = written[serializedPath(r.key)]
		_, err := loadSide(probe)
		if err == nil {
			kept = append(kept, r)
			continue
		}
		d := m.owners[r.path].def
		c := len(m.result.Conflicts)
		m.conflict(r.key, d, interfaces.MERGE_CONFLICT_INVALID_DEFINITION, nil)
		m.result.Conflicts[c].Detail = err.Error()
		m.result.Files[r.path] = d.data
		m.models[r.key] = d.model
		m.count(r.key, &m.result.Summary.Conflicted)
	}
	dropped := len(kept) < len(m.rewritten)
	m.rewritten = kept
	return dropped
}

// network assembles the merged network model for the serializer: the network header and every
// merged definition, which the overview of network.bkn and the concept group tables read.
func (m *merger) network() (*bkn.BknNetwork, error) {
	header, ok := m.models[defKey{kind: kindNetwork}].(*bkn.BknNetwork)
	if !ok {
		return nil, errors.New("merged definitions have no network.bkn")
	}
	net := *header
	keys := make([]defKey, 0, len(m.models))
	for k := range m.models {
		keys = append(keys, k)
	}
	sortKeys(keys)
	for _, k := range keys {
		switch v := m.models[k].(type) {
		case *bkn.BknObjectType:
			net.ObjectTypes = append(net.ObjectTypes, v)
		case *bkn.BknRelationType:
			net.RelationTypes = append(net.RelationTypes, v)
		case *bkn.BknActionType:
			net.ActionTypes = append(net.ActionTypes, v)
		case *bkn.BknRiskType:
			net.RiskTypes = append(net.RiskTypes, v)
		case *bkn.BknConceptGroup:
			net.ConceptGroups = append(net.ConceptGroups, v)
		case *bkn.BknValueType:
			net.ValueTypes = append(net.ValueTypes, v)
		}
	}
	return &net, nil
}

// loadSide parses a bkn/ file set with the SDK loader and keys each definition by kind and id.
// network.bkn is keyed by kind alone, so a network id change merges as a field. Files the loader
// does not read as a definition are kept whole, keyed by path.
func loadSide(files map[string][]byte) (map[defKey]*definition, error) {
	defs := map[defKey]*definition{}
	if len(files) == 0 {
		return defs, nil
	}
	net, err := loadNetwork(files)
	if err != nil {
		return nil, err
	}

	header := *net
	header.ObjectTypes, header.RelationTypes, header.ActionTypes = nil, nil, nil
	header.RiskTypes, header.ConceptGroups, header.ObjectMetrics, header.ValueTypes = nil, nil, nil, nil
	header.SkillContent, header.RawContent = "", ""
	models := map[defKey]any{{kind: kindNetwork}: &header}
	for _, v := range net.ObjectTypes {
		models[defKey{kindObjectType, v.ID}] = v
	}
	for _, v := range net.RelationTypes {
		models[defKey{kindRelationType, v.ID}] = v
	}
	for _, v := range net.ActionTypes {
		models[defKey{kindActionType, v.ID}] = v
	}
	for _, v := range net.RiskTypes {
		models[defKey{kindRiskType, v.ID}] = v
	}
	for _, v := range net.ConceptGroups {
		models[defKey{kindConceptGroup, v.ID}] = v
	}
	for _, v := range net.ValueTypes {
		models[defKey{kindValueType, v.ID}] = v
	}

	for path, data := range files {
		k, isDef, err := definitionKey(path, data)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		model, loaded := models[k]
		if !isDef || !loaded {
			defs[defKey{interfaces.MERGE_TYPE_FILE, path}] = &definition{path: path, data: data, norm: string(data)}
			continue
		}
		if prev, dup := defs[k]; dup {
			return nil, fmt.Errorf("%s %s is defined in both %s and %s", k.kind, k.id, prev.path, path)
		}
		norm, err := normalize(model)
		if err != nil {
			return nil, err
		}
		defs[k] = &definition{path: path, data: data, model: model, norm: norm}
	}
	return defs, nil
}

// definitionKey tells whether path holds a definition and returns its kind and id.
func definitionKey(path string, data []byte) (defKey, bool, error) {
	if path == bkn.RootFileName {
		return defKey{kind: kindNetwork}, true, nil
	}
	dir, name, ok := strings.Cut(path, "/")
	kind, known := kindDirs[dir]
	if !ok || !known || strings.Contains(name, "/") || !strings.HasSuffix(strings.ToLower(name), bkn.ExtensionBkn) {
		return defKey{}, false, nil
	}
	fm, err := bkn.ParseFrontmatter(string(data))
	if err != nil {
		return defKey{}, false, err
	}
	id, _ := fm["id"].(string)
	return defKey{kind, id}, id != "", nil
}

// normalize turns an SDK model into generic JSON for comparison: derived fields are dropped, and
// null, empty arrays and empty objects are left out, as the SDK's diff treats nil and empty alike.
func normalize(model any) (any, error) {
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	for _, f := range ignoredFields {
		delete(v, f)
	}
	return clean(v), nil
}

func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, item := range x {
			if c := clean(item); c == nil {
				delete(x, k)
			} else {
				x[k] = c
			}
		}
		if len(x) == 0 {
			return nil
		}
	case []any:
		if len(x) == 0 {
			return nil
		}
		for i := range x {
			x[i] = clean(x[i])
		}
	}
	return v
}

func equal(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

// decodeModel turns merged generic JSON back into the SDK model of kind.
func decodeModel(kind string, merged any) (any, error) {
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindNetwork:
		v := &bkn.BknNetwork{}
		err = json.Unmarshal(raw, v)
		v.Summary = bkn.ExtractSummary(v.Description)
		return v, err
	case kindObjectType:
		v := &bkn.BknObjectType{}
		err = json.Unmarshal(raw, v)
		v.Summary = bkn.ExtractSummary(v.Description)
		return v, err
	case kindRelationType:
		v := &bkn.BknRelationType{}
		if err = json.Unmarshal(raw, v); err != nil {
			return nil, err
		}
		v.Summary = bkn.ExtractSummary(v.Description)
		v.MappingRules, err = decodeMappingRules(v.Endpoint.Type, v.MappingRules)
		return v, err
	case kindActionType:
		v := &bkn.BknActionType{}
		err = json.Unmarshal(raw, v)
		v.Summary = bkn.ExtractSummary(v.Description)
		return v, err
	case kindRiskType:
		v := &bkn.BknRiskType{}
		err = json.Unmarshal(raw, v)
		v.Summary = bkn.ExtractSummary(v.Description)
		return v, err
	case kindConceptGroup:
		v := &bkn.BknConceptGroup{}
		err = json.Unmarshal(raw, v)
		v.Summary = bkn.ExtractSummary(v.Description)
		return v, err
	case kindValueType:
		v := &bkn.BknValueType{}
		err = json.Unmarshal(raw, v)
		v.Summary = bkn.ExtractSummary(v.Description)
		return v, err
	}
	return nil, fmt.Errorf("unknown definition kind %s", kind)
}

// decodeMappingRules restores the concrete type the SDK keeps in BknRelationType.MappingRules,
// which a JSON round trip turns into generic maps; the serializer switches on that type.
func decodeMappingRules(endpointType string, rules any) (any, error) {
	if rules == nil {
		return nil, nil
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	switch endpointType {
	case bkn.RELATION_MAPPING_TYPE_DIRECT:
		var v bkn.DirectMappingRule
		err = json.Unmarshal(raw, &v)
		return v, err
	case bkn.RELATION_MAPPING_TYPE_INDIRECT:
		v := &bkn.InDirectMappingRule{}
		err = json.Unmarshal(raw, v)
		return v, err
	case bkn.RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN:
		v := &bkn.FilteredCrossJoinMapping{}
		err = json.Unmarshal(raw, v)
		return v, err
	}
	return rules, nil
}

type fieldConflict struct {
	path               string
	base, ours, theirs any
}

// merge3 merges one value. A side that left the value as it was in base takes the other side;
// objects merge key by key and arrays of objects merge element by element on their key field.
// Anything else changed differently on both sides is a conflict, resolved to ours. An absent
// value is nil, so a deletion merges like any other change.
func merge3(path string, base, ours, theirs any, conflicts *[]fieldConflict) any {
	switch {
	case equal(ours, theirs):
		return ours
	case equal(base, ours):
		return theirs
	case equal(base, theirs):
		return ours
	}
	if merged, ok := mergeObjects(path, base, ours, theirs, conflicts); ok {
		return merged
	}
	if merged, ok := mergeKeyedArrays(path, base, ours, theirs, conflicts); ok {
		return merged
	}
	*conflicts = append(*conflicts, fieldConflict{path: path, base: base, ours: ours, theirs: theirs})
	return ours
}

func mergeObjects(path string, base, ours, theirs any, conflicts *[]fieldConflict) (any, bool) {
	o, ok1 := ours.(map[string]any)
	t, ok2 := theirs.(map[string]any)
	b, ok3 := base.(map[string]any)
	if !ok1 || !ok2 || (!ok3 && base != nil) {
		return nil, false
	}
	keys := map[string]bool{}
	for _, m := range []map[string]any{b, o, t} {
		for k := range m {
			keys[k] = true
		}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	merged := map[string]any{}
	for _, k := range sorted {
		if v := merge3(joinPath(path, snakeCase(k)), b[k], o[k], t[k], conflicts); v != nil {
			merged[k] = v
		}
	}
	return clean(merged), true
}

func mergeKeyedArrays(path string, base, ours, theirs any, conflicts *[]fieldConflict) (any, bool) {
	o, ok1 := ours.([]any)
	t, ok2 := theirs.([]any)
	b, ok3 := base.([]any)
	if !ok1 || !ok2 || (!ok3 && base != nil) {
		return nil, false
	}
	field := keyField(b, o, t)
	if field == "" {
		return nil, false
	}
	bIdx, _, okB := indexByKey(b, field)
	oIdx, oOrder, okO := indexByKey(o, field)
	tIdx, tOrder, okT := indexByKey(t, field)
	if !okB || !okO || !okT {
		return nil, false
	}

	// Ours keeps its order; elements only theirs has follow in theirs' order.
	order := append([]string{}, oOrder...)
	for _, k := range tOrder {
		if _, ok := oIdx[k]; !ok {
			order = append(order, k)
		}
	}
	var merged []any
	for _, k := range order {
		if v := merge3(fmt.Sprintf("%s[%s]", path, k), bIdx[k], oIdx[k], tIdx[k], conflicts); v != nil {
			merged = append(merged, v)
		}
	}
	return clean(merged), true
}

// keyField picks the field the elements of the arrays are keyed on, from the first object found.
func keyField(arrays ...[]any) string {
	for _, a := range arrays {
		for _, item := range a {
			obj, ok := item.(map[string]any)
			if !ok {
				return ""
			}
			for _, f := range elementKeyFields {
				if _, ok := obj[f].(string); ok {
					return f
				}
			}
			return ""
		}
	}
	return ""
}

// indexByKey indexes an array of objects by field. It fails when an element is not an object or
// its key is missing or repeated, in which case the array cannot be merged element by element.
func indexByKey(a []any, field string) (map[string]any, []string, bool) {
	idx := make(map[string]any, len(a))
	order := make([]string, 0, len(a))
	for _, item := range a {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, nil, false
		}
		k, _ := obj[field].(string)
		if _, dup := idx[k]; k == "" || dup {
			return nil, nil, false
		}
		idx[k] = obj
		order = append(order, k)
	}
	return idx, order, true
}

// render prints a conflicting value for a reader: a scalar as it is, anything else as JSON.
func render(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// snakeCase spells a Go field name the way the SDK's diff paths do: DataProperties is
// data_properties and an acronym stays together, so KNID is kn_id.
func snakeCase(name string) string {
	var sb strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && (!unicode.IsUpper(runes[i-1]) || (i+1 < len(runes) && !unicode.IsUpper(runes[i+1]))) {
				sb.WriteRune('_')
			}
			sb.WriteRune(unicode.ToLower(r))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// serializedPath is where WriteNetworkToTar writes a definition.
func serializedPath(k defKey) string {
	if k.kind == kindNetwork {
		return bkn.RootFileName
	}
	for dir, kind := range kindDirs {
		if kind == k.kind {
			return dir + "/" + k.id + bkn.ExtensionBkn
		}
	}
	return ""
}

func readTar(r io.Reader) (map[string][]byte, error) {
	files := map[string][]byte{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[hdr.Name] = data
	}
}

func unionKeys(sides ...map[defKey]*definition) []defKey {
	seen := map[defKey]bool{}
	var keys []defKey
	for _, side := range sides {
		for k := range side {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sortKeys(keys)
	return keys
}

func sortKeys(keys []defKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].id < keys[j].id
	})
}
