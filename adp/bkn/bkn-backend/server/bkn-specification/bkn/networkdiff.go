// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"reflect"
	"sort"
)

// FieldChange is one difference inside a definition.
//
// Path is written for a reader, not for a parser: "data_properties[alt_part].description" names
// the property by its own name rather than by a slice index, so the same edit produces the same
// path however the table happens to be ordered.
type FieldChange struct {
	Path string     `json:"path"`
	Kind DiffAction `json:"kind"`
	Old  string     `json:"old,omitempty"`
	New  string     `json:"new,omitempty"`
}

// ValueChange is an old/new pair for a single scalar.
type ValueChange struct {
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// DefinitionDiff is the difference for one definition — one object type, relation type, metric and
// so on.
type DefinitionDiff struct {
	Type   string      `json:"type"`
	ID     string      `json:"id"`
	Action DiffAction  `json:"action"`
	Name   ValueChange `json:"name"`

	// PairedBy is "id" for the normal case and "name" when the two sides were matched by name
	// because their ids differ. A name match is a guess: two definitions can share a name and mean
	// different things, so the ids are carried along for a human to check.
	PairedBy string `json:"paired_by"`
	BaseID   string `json:"base_id,omitempty"`
	TargetID string `json:"target_id,omitempty"`

	Changes []FieldChange `json:"changes,omitempty"`
}

// DiffSummary counts every definition, including the ones left out of Entries.
type DiffSummary struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Deleted   int `json:"deleted"`
	Unchanged int `json:"unchanged"`
}

// Lineage reports how much of the two models' identity actually overlaps.
//
// Two networks built independently share no definition ids at all — ids are generated per
// network — and their diff degrades into "everything deleted, everything created". That result is
// not worth reading definition by definition, so the caller is told before it starts.
type Lineage struct {
	CommonIDs int  `json:"common_ids"`
	TotalIDs  int  `json:"total_ids"`
	Related   bool `json:"related"`
}

// NetworkDiff is the whole comparison of two networks.
type NetworkDiff struct {
	// includeUnchanged is carried from the options so record knows whether an identical entry is
	// counted only or also listed.
	includeUnchanged bool

	Summary DiffSummary      `json:"summary"`
	Lineage Lineage          `json:"lineage"`
	Network *DefinitionDiff  `json:"network,omitempty"`
	Entries []DefinitionDiff `json:"entries"`
}

// DiffOptions tunes how the two sides are paired.
type DiffOptions struct {
	// FallbackByName pairs leftover definitions of the same kind by name once id matching is done.
	// Off by default: it is a heuristic, and a wrong pair reads like a real modification.
	FallbackByName bool

	// IncludeUnchanged also returns the definitions that are identical on both sides, carrying no
	// changes. Off by default because a comparison is usually read for what moved; a caller that
	// lists the whole model beside the differences asks for them.
	IncludeUnchanged bool
}

// definitionRef is one comparable definition lifted out of a network.
type definitionRef struct {
	Kind string
	ID   string
	Name string
	Val  any
}

// DiffNetworkModels compares two parsed networks and reports what changed, down to the field.
//
// Definitions are paired by kind and id. Definitions that are byte-identical are counted in the
// summary and left out of Entries: a network has hundreds of definitions and almost all of them
// are unchanged in any given comparison, so returning them would swamp the response with silence.
func DiffNetworkModels(base, target *BknNetwork, opts DiffOptions) *NetworkDiff {
	result := &NetworkDiff{Entries: []DefinitionDiff{}, includeUnchanged: opts.IncludeUnchanged}
	if base == nil || target == nil {
		return result
	}

	baseDefs := collectDefinitions(base)
	targetDefs := collectDefinitions(target)
	result.Lineage = measureLineage(baseDefs, targetDefs)

	// The root file always differs when two different networks are compared — they carry different
	// names and ids by construction. It is reported on its own so that it never spends one of the
	// "modified" slots that a reader scans for real schema change.
	result.Network = diffNetworkFile(base, target)

	pairedTarget := make(map[string]bool, len(targetDefs))
	targetByKey := make(map[string]definitionRef, len(targetDefs))
	for _, def := range targetDefs {
		targetByKey[def.Kind+":"+def.ID] = def
	}

	var leftoverBase []definitionRef
	for _, baseDef := range baseDefs {
		key := baseDef.Kind + ":" + baseDef.ID
		targetDef, ok := targetByKey[key]
		if !ok {
			leftoverBase = append(leftoverBase, baseDef)
			continue
		}
		pairedTarget[key] = true
		result.record(pairDiff(baseDef, targetDef, "id"))
	}

	var leftoverTarget []definitionRef
	for _, def := range targetDefs {
		if !pairedTarget[def.Kind+":"+def.ID] {
			leftoverTarget = append(leftoverTarget, def)
		}
	}

	if opts.FallbackByName {
		leftoverBase, leftoverTarget = pairLeftoversByName(result, leftoverBase, leftoverTarget)
	}

	// A definition that exists on one side only carries its whole content, field by field, with
	// the other side blank. Reporting only "this exists on one side" leaves a reader looking at an
	// empty panel with no way to see what is actually being added or removed.
	for _, def := range leftoverBase {
		result.record(DefinitionDiff{
			Type: def.Kind, ID: def.ID, Action: DiffDelete, PairedBy: "id",
			Name:    ValueChange{Old: def.Name},
			Changes: describeDefinition(def.Val, DiffDelete),
		})
	}
	for _, def := range leftoverTarget {
		result.record(DefinitionDiff{
			Type: def.Kind, ID: def.ID, Action: DiffCreate, PairedBy: "id",
			Name:    ValueChange{New: def.Name},
			Changes: describeDefinition(def.Val, DiffCreate),
		})
	}

	sort.SliceStable(result.Entries, func(i, j int) bool {
		if result.Entries[i].Type != result.Entries[j].Type {
			return result.Entries[i].Type < result.Entries[j].Type
		}
		return result.Entries[i].ID < result.Entries[j].ID
	})
	return result
}

// record files one definition diff into the summary, keeping unchanged definitions out of Entries.
func (d *NetworkDiff) record(entry DefinitionDiff) {
	switch entry.Action {
	case DiffSkip:
		d.Summary.Unchanged++
		if !d.includeUnchanged {
			return
		}
		// An unchanged entry carries no changes: it is here to be listed, not to be read.
		entry.Changes = nil
		d.Entries = append(d.Entries, entry)
		return
	case DiffCreate:
		d.Summary.Created++
	case DiffUpdate:
		d.Summary.Updated++
	case DiffDelete:
		d.Summary.Deleted++
	}
	d.Entries = append(d.Entries, entry)
}

func pairDiff(base, target definitionRef, pairedBy string) DefinitionDiff {
	entry := DefinitionDiff{
		Type:     base.Kind,
		ID:       target.ID,
		PairedBy: pairedBy,
		Name:     ValueChange{Old: base.Name, New: target.Name},
		Changes:  diffDefinitionValues(base.Val, target.Val),
	}
	if pairedBy == "name" {
		entry.BaseID, entry.TargetID = base.ID, target.ID
	}
	if len(entry.Changes) == 0 && base.ID == target.ID {
		entry.Action = DiffSkip
		return entry
	}
	entry.Action = DiffUpdate
	return entry
}

// pairLeftoversByName matches what id pairing could not, using the definition name.
func pairLeftoversByName(result *NetworkDiff, base, target []definitionRef) ([]definitionRef, []definitionRef) {
	targetByName := make(map[string][]int, len(target))
	for i, def := range target {
		key := def.Kind + ":" + def.Name
		targetByName[key] = append(targetByName[key], i)
	}

	used := make(map[int]bool, len(target))
	var unpairedBase []definitionRef
	for _, baseDef := range base {
		key := baseDef.Kind + ":" + baseDef.Name
		candidates := targetByName[key]
		// An ambiguous name pairs with nothing: guessing between two candidates would invent a
		// modification that never happened.
		if baseDef.Name == "" || len(candidates) != 1 || used[candidates[0]] {
			unpairedBase = append(unpairedBase, baseDef)
			continue
		}
		idx := candidates[0]
		used[idx] = true
		result.record(pairDiff(baseDef, target[idx], "name"))
	}

	var unpairedTarget []definitionRef
	for i, def := range target {
		if !used[i] {
			unpairedTarget = append(unpairedTarget, def)
		}
	}
	return unpairedBase, unpairedTarget
}

func measureLineage(base, target []definitionRef) Lineage {
	baseIDs := make(map[string]bool, len(base))
	for _, def := range base {
		baseIDs[def.Kind+":"+def.ID] = true
	}
	union := make(map[string]bool, len(base)+len(target))
	for key := range baseIDs {
		union[key] = true
	}
	common := 0
	for _, def := range target {
		key := def.Kind + ":" + def.ID
		if baseIDs[key] {
			common++
		}
		union[key] = true
	}
	lineage := Lineage{CommonIDs: common, TotalIDs: len(union)}
	lineage.Related = common > 0
	return lineage
}

func diffNetworkFile(base, target *BknNetwork) *DefinitionDiff {
	entry := DefinitionDiff{
		Type:     "network",
		ID:       target.ID,
		PairedBy: "id",
		Name:     ValueChange{Old: base.Name, New: target.Name},
		BaseID:   base.ID,
		TargetID: target.ID,
	}
	entry.Changes = diffDefinitionValues(networkHeader(base), networkHeader(target))
	if len(entry.Changes) == 0 {
		entry.Action = DiffSkip
		return &entry
	}
	entry.Action = DiffUpdate
	return &entry
}

// networkHeader is the part of the root file that is not just an index of the definitions below
// it. The overview tables restate every object type and relation type, so comparing them would
// report each definition twice.
type networkHeaderView struct {
	ID          string
	Name        string
	Tags        []string
	Version     string
	Branch      string
	Description string
}

func networkHeader(net *BknNetwork) networkHeaderView {
	return networkHeaderView{
		ID:          net.ID,
		Name:        net.Name,
		Tags:        net.Tags,
		Version:     net.Version,
		Branch:      net.Branch,
		Description: net.Description,
	}
}

func collectDefinitions(net *BknNetwork) []definitionRef {
	var defs []definitionRef
	for _, item := range net.ObjectTypes {
		if item != nil {
			defs = append(defs, definitionRef{"object_type", item.ID, item.Name, *item})
		}
	}
	for _, item := range net.RelationTypes {
		if item != nil {
			defs = append(defs, definitionRef{"relation_type", item.ID, item.Name, *item})
		}
	}
	for _, item := range net.ActionTypes {
		if item != nil {
			defs = append(defs, definitionRef{"action_type", item.ID, item.Name, *item})
		}
	}
	for _, item := range net.RiskTypes {
		if item != nil {
			defs = append(defs, definitionRef{"risk_type", item.ID, item.Name, *item})
		}
	}
	for _, item := range net.ConceptGroups {
		if item != nil {
			defs = append(defs, definitionRef{"concept_group", item.ID, item.Name, *item})
		}
	}
	for _, item := range net.Metrics {
		if item != nil {
			defs = append(defs, definitionRef{"metric", item.ID, item.Name, *item})
		}
	}
	return defs
}

// describeDefinition renders a whole definition as one-sided changes, so a created or deleted
// definition reads the same way as a modified one.
func describeDefinition(value any, kind DiffAction) []FieldChange {
	var changes []FieldChange
	expandOneSided("", reflect.ValueOf(value), kind, &changes)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
