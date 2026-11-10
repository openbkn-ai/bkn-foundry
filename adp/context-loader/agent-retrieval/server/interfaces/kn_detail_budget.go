// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "strings"

// DetailLevelBrief answers with the network's navigation shell on purpose: its name,
// description, concept groups and how many concepts of each kind it holds. It is the
// cheap way to learn what a network is about before reading any of its model.
const DetailLevelBrief = "brief"

// MaxKnDetailChars bounds a get_kn_detail answer, counted in characters of the text
// the caller reads, in the format they asked for.
//
// The count caps above bound what an answer costs to build; this bounds what it costs
// to read. Under them a summary still reached about 218 thousand characters, which
// agent hosts store to a file, cut off, or refuse, so the caller lost part of the
// model without being told which part. 30000 characters is about twenty thousand
// tokens even for Chinese text, under the 25000-token result limit common to MCP
// hosts. It is a variable only so tests written against the count caps can set it
// aside.
var MaxKnDetailChars = 30000

// KnDetailReduction names how far an answer was narrowed to fit MaxKnDetailChars.
type KnDetailReduction string

const (
	// KnDetailReductionNone is the answer at the level asked for.
	KnDetailReductionNone KnDetailReduction = ""
	// KnDetailReductionOutline keeps every concept but none of the object type
	// properties, which are most of a summary's size.
	KnDetailReductionOutline KnDetailReduction = "outline"
	// KnDetailReductionShell is the navigation shell.
	KnDetailReductionShell KnDetailReduction = "shell"
)

// ReduceToOutline keeps every object, relation and action type, and of each only what
// a caller chooses it by: ids, names, tags, endpoints, lifecycle and counts. The
// property lists are replaced by their sizes, and get_object_types returns them for
// the object types the caller picks.
func (d *KnowledgeNetworkDetail) ReduceToOutline() {
	if d == nil {
		return
	}
	for _, o := range d.ObjectTypes {
		if o == nil {
			continue
		}
		o.DataPropertyCount = len(o.DataProperties)
		o.LogicPropertyCount = len(o.LogicProperties)
		o.DataProperties = nil
		o.LogicProperties = nil
		// Which properties the caller may read is a statement about properties this
		// answer no longer lists.
		o.EffectivePermissions = nil
		o.RelatedMetrics = nil
		o.ModuleType = ""
		o.Comment = ""
	}
	for _, r := range d.RelationTypes {
		if r == nil {
			continue
		}
		r.ModuleType = ""
		r.Comment = ""
		r.MappingRules = nil
		r.SourceObjectType = nil
		r.TargetObjectType = nil
	}
	for _, a := range d.ActionTypes {
		if a == nil {
			continue
		}
		a.ModuleType = ""
		a.Comment = ""
	}
}

// IsNavigationShell reports whether d is already the navigation shell.
func (d *KnowledgeNetworkDetail) IsNavigationShell() bool {
	return d != nil && d.navigationShell
}

// FitWithin narrows d one step at a time until size says it is at most limit
// characters: first to its outline, then to its navigation shell. Each step sets the
// notice that step calls for before it is measured, so the limit covers the notice.
//
// The shell is the last step and is returned even when it is still larger than the
// limit, but before that it drops each concept group's object type ids, the one part
// of the shell that grows with the network.
func (d *KnowledgeNetworkDetail) FitWithin(limit int, notice func(KnDetailReduction) string,
	size func(*KnowledgeNetworkDetail) (int, error)) (KnDetailReduction, error) {
	if d == nil {
		return KnDetailReductionNone, nil
	}
	n, err := size(d)
	if err != nil || n <= limit {
		return KnDetailReductionNone, err
	}
	if !d.navigationShell {
		d.ReduceToOutline()
		d.Notice = notice(KnDetailReductionOutline)
		if n, err = size(d); err != nil || n <= limit {
			return KnDetailReductionOutline, err
		}
		d.ReduceToNavigationShell()
		d.Notice = notice(KnDetailReductionShell)
		if n, err = size(d); err != nil || n <= limit {
			return KnDetailReductionShell, err
		}
	}
	for _, g := range d.ConceptGroups {
		if g != nil {
			g.ObjectTypeIDs = nil
		}
	}
	return KnDetailReductionShell, nil
}

// KnDetailNotice names the localized notice for an answer narrowed by reduction, or
// for a shell built because the caller asked for brief or the network passed a count
// cap, and the arguments it renders with.
func (d *KnowledgeNetworkDetail) KnDetailNotice(reduction KnDetailReduction, detailLevel, branch string) (key string, args map[string]any) {
	args = map[string]any{
		"Arg1": d.ObjectTypeCount,
		"Arg2": d.RelationTypeCount,
		"Arg3": MaxSummaryObjectTypes,
		"Arg4": MaxSummaryRelationTypes,
		"Arg5": MaxKnDetailChars,
	}
	switch {
	case reduction == KnDetailReductionOutline:
		return "KnDetailOutline", args
	case detailLevel == DetailLevelBrief:
		return "KnDetailBrief", args
	}
	if selected := strings.TrimSpace(branch); selected != "" && selected != "main" {
		return "KnDetailNavigationShellBranch", args
	}
	return d.NavigationShellNoticeKey(), args
}
