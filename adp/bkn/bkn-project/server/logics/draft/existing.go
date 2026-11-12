// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const relationTypesDir = "relation_types/"

// GetExisting reads the project's branch: the object and relation types there that the draft
// did not write are the network's own.
func (s *draftService) GetExisting(ctx context.Context, token string, projectID string) (*interfaces.DraftExisting, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetExistingDefinitions")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	var owned []string
	if draft, exist, err := s.da.GetDraft(ctx, projectID); err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	} else if exist {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	existing, err := s.readExisting(ctx, entry.KNID, entry.Branch, owned)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return nil, internalError(ctx, "Read branch failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return existing, nil
}

// ownedPaths are the paths a draft's confirmations wrote or were about to write.
func ownedPaths(confirmation *interfaces.DraftConfirmation) []string {
	if confirmation == nil {
		return nil
	}
	return unionSorted(confirmation.Files, confirmation.Pending)
}

// readExisting parses the definitions of the branch outside owned. A file that does not parse is
// left out, as the release review leaves it.
func (s *draftService) readExisting(ctx context.Context, lineageID string, branch string,
	owned []string) (*interfaces.DraftExisting, error) {
	files, err := s.gs.ReadTree(ctx, lineageID, "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	return existingOf(files, owned), nil
}

// existingDefinitions parses the object and relation types of the branch outside owned, by id. The
// branch is read as a whole, under the specification its network.bkn declares, so that what 3.0
// declares, such as aliases, is read too; a branch the loader refuses is read file by file instead,
// leaving out a file that does not parse, as the release review leaves it.
func existingDefinitions(files map[string][]byte, owned []string) ([]*bkn.BknObjectType, []*bkn.BknRelationType) {
	skip := make(map[string]bool, len(owned))
	for _, p := range owned {
		skip[p] = true
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	objectTypes, relationTypes := []*bkn.BknObjectType{}, []*bkn.BknRelationType{}
	ownedObjects, ownedRelations := map[string]bool{}, map[string]bool{}
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, objectTypesDir):
			if ot, err := bkn.ParseObjectTypeFile(string(files[p]), p); err == nil && ot.ID != "" {
				if skip[p] {
					ownedObjects[ot.ID] = true
				} else {
					objectTypes = append(objectTypes, ot)
				}
			}
		case strings.HasPrefix(p, relationTypesDir):
			if rt, err := bkn.ParseRelationTypeFile(string(files[p]), p); err == nil && rt.ID != "" {
				if skip[p] {
					ownedRelations[rt.ID] = true
				} else {
					relationTypes = append(relationTypes, rt)
				}
			}
		}
	}
	if _, ok := files[bkn.RootFileName]; !ok {
		return objectTypes, relationTypes
	}
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	network, err := bkn.LoadNetworkDefinitionsWithFS(fsys, ".")
	if err != nil {
		return objectTypes, relationTypes
	}
	objectTypes, relationTypes = []*bkn.BknObjectType{}, []*bkn.BknRelationType{}
	for _, ot := range network.ObjectTypes {
		if ot.ID != "" && !ownedObjects[ot.ID] {
			objectTypes = append(objectTypes, ot)
		}
	}
	for _, rt := range network.RelationTypes {
		if rt.ID != "" && !ownedRelations[rt.ID] {
			relationTypes = append(relationTypes, rt)
		}
	}
	slices.SortFunc(objectTypes, func(a, b *bkn.BknObjectType) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(relationTypes, func(a, b *bkn.BknRelationType) int { return strings.Compare(a.ID, b.ID) })
	return objectTypes, relationTypes
}

func existingOf(files map[string][]byte, owned []string) *interfaces.DraftExisting {
	objectTypes, relationTypes := existingDefinitions(files, owned)
	existing := &interfaces.DraftExisting{
		ObjectTypes:   []*interfaces.DraftExistingObjectType{},
		RelationTypes: []*interfaces.DraftExistingRelationType{},
	}
	for _, ot := range objectTypes {
		item := &interfaces.DraftExistingObjectType{
			ID: ot.ID, Name: ot.Name, Aliases: ot.Aliases, Description: ot.Description,
			PrimaryKeys: ot.PrimaryKeys, PropertyCount: len(ot.DataProperties),
		}
		if ot.DataSource != nil {
			item.TableID = ot.DataSource.ID
		}
		if item.PrimaryKeys == nil {
			item.PrimaryKeys = []string{}
		}
		existing.ObjectTypes = append(existing.ObjectTypes, item)
	}
	for _, rt := range relationTypes {
		existing.RelationTypes = append(existing.RelationTypes, &interfaces.DraftExistingRelationType{
			ID: rt.ID, Name: rt.Name, Aliases: rt.Aliases, Source: rt.Endpoint.Source, Target: rt.Endpoint.Target,
		})
	}
	return existing
}

// existingItemsOf reads the object and relation types of the branch outside owned as draft items
// marked existing: adopted, with nothing for a person to decide, keeping their ids. The draft shows
// them beside its own items and relates new items to them; it never edits or writes them.
func existingItemsOf(files map[string][]byte, owned []string) interfaces.DraftItems {
	objectTypes, relationTypes := existingDefinitions(files, owned)
	items := interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{}, RelationTypes: []*interfaces.DraftRelationType{},
		ValueTypes: []*interfaces.DraftValueType{}, Excluded: []*interfaces.DraftExcludedField{},
		Unreadable: []*interfaces.DraftTable{},
	}
	for _, ot := range objectTypes {
		items.ObjectTypes = append(items.ObjectTypes, existingObjectType(ot))
	}
	for _, rt := range relationTypes {
		items.RelationTypes = append(items.RelationTypes, existingRelationType(rt))
	}
	return items
}

func existingObjectType(ot *bkn.BknObjectType) *interfaces.DraftObjectType {
	item := &interfaces.DraftObjectType{
		ID: ot.ID, Name: ot.Name, Aliases: ot.Aliases, Description: ot.Description, Adopted: true, Existing: true,
		PrimaryKeys: slices.Clone(ot.PrimaryKeys), DisplayKey: ot.DisplayKey, IncrementalKey: ot.IncrementalKey,
		Properties: []*interfaces.DraftProperty{},
		Key:        interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_CHOSEN},
	}
	if item.PrimaryKeys == nil {
		item.PrimaryKeys = []string{}
	}
	if ot.DataSource != nil {
		item.Table = interfaces.DraftTable{ResourceID: ot.DataSource.ID, Name: ot.DataSource.Name}
	}
	for _, p := range ot.DataProperties {
		property := &interfaces.DraftProperty{Name: p.Name, DisplayName: p.DisplayName, Aliases: p.Aliases,
			Type: p.Type, Description: p.Description, MappedField: p.MappedField}
		if p.MappedField != "" {
			property.SourceOfRecord = interfaces.SOURCE_OF_RECORD_TABLE
		}
		if p.Constraints != nil {
			property.ValueTypeID = p.Constraints.ValueType
		}
		item.Properties = append(item.Properties, property)
	}
	return item
}

func existingRelationType(rt *bkn.BknRelationType) *interfaces.DraftRelationType {
	item := &interfaces.DraftRelationType{
		ID: rt.ID, Name: rt.Name, Aliases: rt.Aliases, Description: rt.Description,
		Source: rt.Endpoint.Source, Target: rt.Endpoint.Target, Cardinality: rt.Endpoint.Cardinality,
		Verification: rt.Verification, Mapping: []interfaces.DraftFieldMapping{}, Adopted: true, Existing: true,
	}
	switch rules := rt.MappingRules.(type) {
	case bkn.DirectMappingRule:
		item.Mapping = draftMapping(rules)
	case *bkn.InDirectMappingRule:
		if rules.BackingDataSource != nil {
			item.LinkTable = &interfaces.DraftTable{ResourceID: rules.BackingDataSource.ID, Name: rules.BackingDataSource.Name}
		}
		item.SourceMapping, item.TargetMapping = draftMapping(rules.SourceMappingRules), draftMapping(rules.TargetMappingRules)
	}
	return item
}

func draftMapping(rules []bkn.MappingRule) []interfaces.DraftFieldMapping {
	mapping := make([]interfaces.DraftFieldMapping, 0, len(rules))
	for _, r := range rules {
		mapping = append(mapping, interfaces.DraftFieldMapping{SourceProperty: r.SourceProperty, TargetProperty: r.TargetProperty})
	}
	return mapping
}

// existingItems reads what the network already has as draft items, leaving out what owned names
// as the draft's own. A project without a git store, as in tests, has none.
func (s *draftService) existingItems(ctx context.Context, entry *interfaces.ProjectEntry,
	owned []string) (interfaces.DraftItems, error) {
	if s.gs == nil || entry == nil || entry.Project == nil {
		return interfaces.DraftItems{}, nil
	}
	files, err := s.gs.ReadTree(ctx, entry.KNID, "refs/heads/"+entry.Branch)
	if err != nil {
		return interfaces.DraftItems{}, err
	}
	return existingItemsOf(files, owned), nil
}

// existingItemsFor reads what the network already has, leaving out what the project's current
// draft wrote.
func (s *draftService) existingItemsFor(ctx context.Context, entry *interfaces.ProjectEntry) (interfaces.DraftItems, error) {
	if s.gs == nil || entry == nil || entry.Project == nil {
		return interfaces.DraftItems{}, nil
	}
	var owned []string
	draft, exist, err := s.da.GetDraft(ctx, entry.ID)
	if err != nil {
		return interfaces.DraftItems{}, err
	}
	if exist {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	return s.existingItems(ctx, entry, owned)
}

// existingTableIDs are the tables the existing object types read.
func existingTableIDs(existing *interfaces.DraftItems) map[string]bool {
	ids := map[string]bool{}
	for _, ot := range existing.ObjectTypes {
		if ot.Existing && ot.Table.ResourceID != "" {
			ids[ot.Table.ResourceID] = true
		}
	}
	return ids
}

// withExisting adds to items what the network already has and they do not hold yet: an existing
// object type unless one of items has its id or reads its table, an existing relation type unless
// one has its id. Items made before the draft read the network's own get them so.
func withExisting(items interfaces.DraftItems, existing interfaces.DraftItems) interfaces.DraftItems {
	if len(existing.ObjectTypes) == 0 && len(existing.RelationTypes) == 0 {
		return items
	}
	ids, tables := map[string]bool{}, map[string]bool{}
	for _, ot := range items.ObjectTypes {
		ids[ot.ID] = true
		tables[ot.Table.ResourceID] = true
	}
	with := items
	with.ObjectTypes = slices.Clone(items.ObjectTypes)
	for _, ot := range existing.ObjectTypes {
		if !ids[ot.ID] && (ot.Table.ResourceID == "" || !tables[ot.Table.ResourceID]) {
			with.ObjectTypes = append(with.ObjectTypes, ot)
		}
	}
	relationIDs := map[string]bool{}
	for _, rt := range items.RelationTypes {
		relationIDs[rt.ID] = true
	}
	with.RelationTypes = slices.Clone(items.RelationTypes)
	for _, rt := range existing.RelationTypes {
		if !relationIDs[rt.ID] {
			with.RelationTypes = append(with.RelationTypes, rt)
		}
	}
	return with
}

// relatedToExisting gives what drafting the new tables beside the tables existing object types
// read drafted: the existing items, then the items of the new tables and their relations to
// existing object types, as adding catalogs merges them. What the existing tables drafted is not
// kept, nor are the tables drafting could not read among them.
func relatedToExisting(existing interfaces.DraftItems, drafted interfaces.DraftItems,
	tables []*interfaces.VegaTable) interfaces.DraftItems {
	used := existingTableIDs(&existing)
	unreadable := []*interfaces.DraftTable{}
	for _, t := range drafted.Unreadable {
		if !used[t.ResourceID] {
			unreadable = append(unreadable, t)
		}
	}
	return mergeDrafted(cloneItems(existing), drafted, tables, unreadable, nil)
}

// recordedOf are the ids drafted for the tables given, as those of existing tables stand as given.
func recordedOf(recorded []*interfaces.RegisteredID, tables []*interfaces.VegaTable) []*interfaces.RegisteredID {
	kept := []*interfaces.RegisteredID{}
	for _, id := range recorded {
		for _, t := range tables {
			if id.SourceKey == t.ID || strings.HasPrefix(id.SourceKey, t.ID+"/") {
				kept = append(kept, id)
				break
			}
		}
	}
	return kept
}

// withoutExistingTables leaves out the tables an existing object type reads, so drafting does not
// model them a second time, and names the ones it left out.
func withoutExistingTables(tables []*interfaces.VegaTable, unreadable []*interfaces.DraftTable,
	used map[string]bool) ([]*interfaces.VegaTable, []*interfaces.DraftTable, []string) {
	if len(used) == 0 {
		return tables, unreadable, nil
	}
	kept := make([]*interfaces.VegaTable, 0, len(tables))
	left := []string{}
	for _, t := range tables {
		if used[t.ID] {
			left = append(left, t.Name)
			continue
		}
		kept = append(kept, t)
	}
	keptUnreadable := make([]*interfaces.DraftTable, 0, len(unreadable))
	for _, t := range unreadable {
		if used[t.ResourceID] {
			continue
		}
		keptUnreadable = append(keptUnreadable, t)
	}
	sort.Strings(left)
	return kept, keptUnreadable, left
}
