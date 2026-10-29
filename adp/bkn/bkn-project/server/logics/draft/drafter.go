// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Reasons a count was not computed.
const (
	reasonCompositeKey = "composite_key"
	reasonCrossCatalog = "cross_catalog"
	reasonLinkTable    = "link_table"
	reasonQueryFailed  = "query_failed"
)

// Why a drafted relation is proposed rather than adopted, stored as codes for the client to word.
const (
	proposedNotCounted       = "not_counted"
	proposedBelowSuggestRate = "below_suggest_hit_rate"
	proposedBelowAdoptRate   = "below_adopt_hit_rate"
	proposedFewValues        = "few_values"
	proposedEndNotAdopted    = "end_not_adopted"
)

// drafter drafts an ontology from the structure of readable tables.
type drafter struct {
	setting common.DraftSetting
	counter counter

	kinds       map[string]string
	objectTypes map[string]*interfaces.DraftObjectType // by table id
	fields      map[string]map[string]string           // table id -> field -> property name
	valueTypes  map[string]*interfaces.DraftValueType  // by code table id
	// keys holds each entity table's key fields: declared, or inferred once counts confirm them.
	keys         map[string][]string
	bySourceName map[string]*interfaces.VegaTable // catalog|schema.name -> table
	items        interfaces.DraftItems

	// projectID and registered are the project's registered ids; recorded are the ids this draft
	// registers anew or in place of a placeholder.
	projectID  string
	registered map[interfaces.IDKey]*interfaces.RegisteredID
	recorded   []*interfaces.RegisteredID
	// named are the keys of the items this draft named from names that are not ASCII.
	named map[interfaces.IDKey]bool
	// namingLanguage is the project's: the language items are named in.
	namingLanguage string
}

func newDrafter(setting common.DraftSetting, c counter) *drafter {
	return &drafter{
		setting:      setting,
		counter:      c,
		kinds:        map[string]string{},
		objectTypes:  map[string]*interfaces.DraftObjectType{},
		fields:       map[string]map[string]string{},
		valueTypes:   map[string]*interfaces.DraftValueType{},
		keys:         map[string][]string{},
		bySourceName: map[string]*interfaces.VegaTable{},
		items: interfaces.DraftItems{
			ObjectTypes:   []*interfaces.DraftObjectType{},
			RelationTypes: []*interfaces.DraftRelationType{},
			ValueTypes:    []*interfaces.DraftValueType{},
			Excluded:      []*interfaces.DraftExcludedField{},
			Unreadable:    []*interfaces.DraftTable{},
		},
	}
}

// sourceName keys a physical table within its catalog: two catalogs may both have erp.customers.
func sourceName(catalogID string, schema string, name string) string {
	return catalogID + "|" + schema + "." + name
}

func draftTable(table *interfaces.VegaTable) interfaces.DraftTable {
	return interfaces.DraftTable{ResourceID: table.ID, Name: table.Name, CatalogID: table.CatalogID}
}

// draft runs every rule over the tables, in order: classify, object types and their keys,
// enums from code tables, then relations. A failed count is recorded on its item; only a
// cancelled context stops drafting.
func (d *drafter) draft(ctx context.Context, tables []*interfaces.VegaTable,
	unreadable []*interfaces.DraftTable) (interfaces.DraftItems, error) {
	d.items.Unreadable = append(d.items.Unreadable, unreadable...)
	// One order for the same tables however they were listed: which of two tables of one name
	// takes the plain ID must not depend on the listing.
	sort.Slice(tables, func(i, j int) bool {
		a, b := tables[i], tables[j]
		// By the physical name first: the resource's name is semantic understanding's to rewrite,
		// and which table takes a plain id must not change with it.
		return cmp.Or(
			cmp.Compare(physicalName(a), physicalName(b)),
			cmp.Compare(a.CatalogID, b.CatalogID),
			cmp.Compare(a.SourceSchema, b.SourceSchema),
			cmp.Compare(a.ID, b.ID),
		) < 0
	})
	for _, table := range tables {
		d.kinds[table.ID] = classify(table, d.setting)
		d.bySourceName[sourceName(table.CatalogID, table.SourceSchema, table.SourceName)] = table
	}

	typeIDs := map[string]bool{}
	d.reserve(interfaces.ID_KIND_OBJECT_TYPES, "", typeIDs)
	for _, table := range asciiFirst(tables) {
		if d.kinds[table.ID] == tableKindEntity {
			d.addObjectType(table, typeIDs)
		}
	}
	for _, table := range tables {
		if d.kinds[table.ID] != tableKindEntity {
			continue
		}
		if err := d.checkKey(ctx, table); err != nil {
			return d.items, err
		}
	}

	valueTypeIDs := map[string]bool{}
	d.reserve(interfaces.ID_KIND_VALUE_TYPES, "", valueTypeIDs)
	for _, table := range asciiFirst(tables) {
		if d.kinds[table.ID] == tableKindCodeTable {
			if err := d.addValueType(ctx, table, valueTypeIDs); err != nil {
				return d.items, err
			}
		}
	}

	relationIDs := map[string]bool{}
	d.reserve(interfaces.ID_KIND_RELATION_TYPES, "", relationIDs)
	// Relations from link tables named otherwise come last: their short names take only what the
	// relations named in ASCII leave.
	namedLater := []*interfaces.VegaTable{}
	for _, table := range tables {
		var err error
		switch d.kinds[table.ID] {
		case tableKindEntity:
			err = d.addRelations(ctx, table, tables, relationIDs)
		case tableKindLinkTable:
			if isASCII(physicalName(table)) {
				d.addLinkRelation(table, relationIDs)
			} else {
				namedLater = append(namedLater, table)
			}
		}
		if err != nil {
			return d.items, err
		}
	}
	for _, table := range namedLater {
		d.addLinkRelation(table, relationIDs)
	}
	for _, valueType := range d.valueTypes {
		valueType.Adopted = len(valueType.UsedBy) > 0
	}
	// A relation type is adopted only when both its object types are, as people's edits keep it.
	adopted := map[string]bool{}
	for _, objectType := range d.items.ObjectTypes {
		adopted[objectType.ID] = objectType.Adopted
	}
	for _, relation := range d.items.RelationTypes {
		if relation.Adopted && !(adopted[relation.Source] && adopted[relation.Target]) {
			relation.Adopted, relation.ProposedBecause = false, proposedEndNotAdopted
		}
	}
	return d.items, nil
}

func (d *drafter) addObjectType(table *interfaces.VegaTable, typeIDs map[string]bool) {
	name, aliases := d.tableName(table)
	objectType := &interfaces.DraftObjectType{
		ID:          d.itemID(interfaces.ID_KIND_OBJECT_TYPES, table.ID, physicalName(table), table.APIName, idPrefixObjectType, typeIDs),
		Name:        name,
		Aliases:     aliases,
		Description: table.Description,
		Table:       draftTable(table),
		Properties:  []*interfaces.DraftProperty{},
	}
	key, source := tableKey(table)
	inKey := map[string]bool{}
	for _, field := range key {
		inKey[field] = true
	}
	inForeignKey := map[string]bool{}
	for _, fk := range table.ForeignKeys {
		for _, field := range fk.Columns {
			inForeignKey[field] = true
		}
	}

	columns := []interfaces.VegaColumn{}
	for _, c := range table.Columns {
		bknType := bknTypes[c.Type]
		if bknType == "" {
			d.items.Excluded = append(d.items.Excluded, &interfaces.DraftExcludedField{
				ObjectType: objectType.ID, Field: c.Name, Reason: excludedUnsupported, Keyword: c.Type,
			})
			continue
		}
		// Keys and foreign keys carry identity and relations; privacy rules never drop them.
		if keyword := privacyKeyword(c, d.setting.PrivacyKeywords); keyword != "" && !inKey[c.Name] && !inForeignKey[c.Name] {
			d.items.Excluded = append(d.items.Excluded, &interfaces.DraftExcludedField{
				ObjectType: objectType.ID, Field: c.Name, Reason: excludedPrivacy, Keyword: keyword,
			})
			continue
		}
		columns = append(columns, c)
	}
	// Fields named in ASCII take their names first, as they always did; short names of the others
	// take what is left. The properties keep the order of the fields.
	names := map[string]bool{}
	d.reserve(interfaces.ID_KIND_PROPERTIES, propertySourceKey(table.ID, ""), names)
	fields := map[string]string{}
	for _, ascii := range []bool{true, false} {
		for _, c := range columns {
			if isASCII(c.Name) == ascii {
				fields[c.Name] = d.itemID(interfaces.ID_KIND_PROPERTIES, propertySourceKey(table.ID, c.Name), c.Name,
					c.APIName, idPrefixProperty, names)
			}
		}
	}
	for _, c := range columns {
		objectType.Properties = append(objectType.Properties, newProperty(c, fields[c.Name], d.language()))
	}
	dropAliasesOfOtherProperties(objectType.Properties)

	objectType.Key = interfaces.DraftKeyEvidence{Source: source}
	if source == interfaces.DRAFT_KEY_NONE && len(identityCandidates(table)) > 0 {
		// An identity is inferred later, once a candidate counts unique and non-null.
		objectType.Key.Source = interfaces.DRAFT_KEY_INFERRED
	}
	for _, field := range key {
		name, ok := fields[field]
		c, _ := column(table, field)
		if !ok || !keyTypes[bknTypes[c.Type]] {
			// A key field that cannot be mapped, or whose type cannot key a BKN object type.
			objectType.PrimaryKeys = nil
			break
		}
		objectType.PrimaryKeys = append(objectType.PrimaryKeys, name)
	}
	objectType.NeedsIdentity = len(objectType.PrimaryKeys) == 0
	objectType.Adopted = !objectType.NeedsIdentity
	if !objectType.NeedsIdentity {
		d.keys[table.ID] = key
	}
	objectType.DisplayKey = displayKey(table, fields, objectType.PrimaryKeys, d.setting.NameFieldKeywords)

	d.objectTypes[table.ID] = objectType
	d.fields[table.ID] = fields
	d.items.ObjectTypes = append(d.items.ObjectTypes, objectType)
}

// newProperty maps a field of a BKN type to a property named name.
func newProperty(c interfaces.VegaColumn, name string, language string) *interfaces.DraftProperty {
	displayName, aliases := fieldNames(c, language)
	return &interfaces.DraftProperty{
		Name:               name,
		DisplayName:        displayName,
		Aliases:            aliases,
		Type:               bknTypes[c.Type],
		Description:        c.Description,
		MappedField:        c.Name,
		SourceOfRecord:     interfaces.SOURCE_OF_RECORD_TABLE,
		MissingDescription: c.Description == "",
	}
}

// fieldNames are the display name of a field in the naming language and its aliases, from the
// business name semantic understanding gave it, its description and its comment, in that order,
// then its own name.
func fieldNames(c interfaces.VegaColumn, language string) (string, []string) {
	return pickName(language, c.Name, c.DisplayName, firstClause(c.Description), firstClause(c.Comment))
}

// displayKey is the first mapped name field, else the first key property.
func displayKey(table *interfaces.VegaTable, fields map[string]string, primaryKeys []string,
	nameKeywords []string) string {
	for _, c := range table.Columns {
		if name, ok := fields[c.Name]; ok && isNameField(c, nameKeywords) {
			return name
		}
	}
	if len(primaryKeys) > 0 {
		return primaryKeys[0]
	}
	return ""
}

// checkKey counts the key over the full table: a key with duplicates or nulls needs a person to
// decide the identity. A table without a declared key takes the first identity candidate that
// counts unique and non-null.
func (d *drafter) checkKey(ctx context.Context, table *interfaces.VegaTable) error {
	objectType := d.objectTypes[table.ID]
	if objectType.Key.Source == interfaces.DRAFT_KEY_INFERRED {
		return d.inferKey(ctx, table, objectType)
	}
	key := d.keys[table.ID]
	switch {
	case objectType.NeedsIdentity:
		return nil
	case len(key) != 1:
		objectType.Key.Reason = reasonCompositeKey
		return nil
	}
	rows, distinct, nulls, err := d.counter.keyStats(ctx, table, key[0])
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		objectType.Key.Reason = fmt.Sprintf("%s: %v", reasonQueryFailed, err)
		return nil
	}
	objectType.Key.Computed, objectType.Key.Rows, objectType.Key.Distinct, objectType.Key.Nulls = true, rows, distinct, nulls
	if nulls > 0 || distinct < rows-nulls {
		objectType.NeedsIdentity, objectType.Adopted = true, false
		delete(d.keys, table.ID)
	}
	return nil
}

// inferKey tries the identity candidates in order; the counts of the last one tried stay as
// the evidence when none qualifies.
func (d *drafter) inferKey(ctx context.Context, table *interfaces.VegaTable, objectType *interfaces.DraftObjectType) error {
	// No candidate qualifying leaves no key source; the counts of the last one tried stay.
	defer func() {
		if objectType.NeedsIdentity {
			objectType.Key.Source = interfaces.DRAFT_KEY_NONE
		}
	}()
	for _, field := range identityCandidates(table) {
		property, ok := d.fields[table.ID][field]
		if !ok {
			continue
		}
		rows, distinct, nulls, err := d.counter.keyStats(ctx, table, field)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// The evidence is this candidate's alone, not an earlier one's counts.
			objectType.Key = interfaces.DraftKeyEvidence{
				Source: interfaces.DRAFT_KEY_INFERRED, Field: field, Reason: fmt.Sprintf("%s: %v", reasonQueryFailed, err),
			}
			continue
		}
		objectType.Key = interfaces.DraftKeyEvidence{
			Source: interfaces.DRAFT_KEY_INFERRED, Field: field, Computed: true, Rows: rows, Distinct: distinct, Nulls: nulls,
		}
		if rows > 0 && nulls == 0 && distinct == rows {
			objectType.PrimaryKeys = []string{property}
			objectType.NeedsIdentity, objectType.Adopted = false, true
			if objectType.DisplayKey == "" {
				objectType.DisplayKey = property
			}
			d.keys[table.ID] = []string{field}
			return nil
		}
	}
	return nil
}

func (d *drafter) addValueType(ctx context.Context, table *interfaces.VegaTable, ids map[string]bool) error {
	key, _ := tableKey(table)
	name := codeTableName(table, d.setting)
	c, _ := column(table, key[0])
	values, err := d.counter.codeValues(ctx, table, key[0], name, d.setting.CodeTableMaxRows+1)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.items.Unreadable = append(d.items.Unreadable, &interfaces.DraftTable{
			ResourceID: table.ID, Name: table.Name, CatalogID: table.CatalogID,
			Reason: fmt.Sprintf("%s: %v", reasonQueryFailed, err),
		})
		return nil
	}
	if int64(len(values)) > d.setting.CodeTableMaxRows {
		// More values than a code table holds: the row count was stale; leave the table out.
		return nil
	}
	// BKN has no aliases of value types: the name alone.
	valueTypeName, _ := d.tableName(table)
	valueType := &interfaces.DraftValueType{
		ID:        d.itemID(interfaces.ID_KIND_VALUE_TYPES, table.ID, physicalName(table), table.APIName, idPrefixValueType, ids),
		Name:      valueTypeName,
		BaseType:  bknTypes[c.Type],
		Values:    values,
		CodeTable: draftTable(table),
		UsedBy:    []interfaces.DraftPropertyRef{},
	}
	d.valueTypes[table.ID] = valueType
	d.items.ValueTypes = append(d.items.ValueTypes, valueType)
	return nil
}

// target finds the table a foreign key refers to, in the referring table's catalog. Connectors
// that know schemas (PostgreSQL) write the referenced table as schema.table; the others write
// the bare name, in the referring table's schema.
func (d *drafter) target(source *interfaces.VegaTable, fk interfaces.VegaForeignKey) *interfaces.VegaTable {
	if schema, name, ok := strings.Cut(fk.RefTable, "."); ok {
		return d.bySourceName[sourceName(source.CatalogID, schema, name)]
	}
	return d.bySourceName[sourceName(source.CatalogID, source.SourceSchema, fk.RefTable)]
}

func (d *drafter) addRelations(ctx context.Context, table *interfaces.VegaTable, tables []*interfaces.VegaTable,
	ids map[string]bool) error {
	source := d.objectTypes[table.ID]
	linked := map[string]bool{}
	for _, fk := range table.ForeignKeys {
		if len(fk.Columns) != 1 || len(fk.RefColumns) != 1 {
			continue
		}
		target := d.target(table, fk)
		if target == nil {
			continue
		}
		linked[fk.Columns[0]] = true
		if _, err := d.refer(ctx, table, source, fk.Columns[0], target, fk.RefColumns[0],
			interfaces.DRAFT_ORIGIN_FOREIGN_KEY, ids); err != nil {
			return err
		}
	}

	inKey := map[string]bool{}
	for _, field := range d.keys[table.ID] {
		inKey[field] = true
	}
	for _, c := range table.Columns {
		if linked[c.Name] || inKey[c.Name] {
			continue
		}
		// Every table the field's name points at, the most specific match first, until one
		// relation or enum is drafted.
		type candidate struct {
			table *interfaces.VegaTable
			key   string
			rank  int
		}
		candidates := []candidate{}
		for _, other := range tables {
			if other.ID == table.ID || d.kinds[other.ID] == tableKindLinkTable {
				continue
			}
			otherKey := d.keys[other.ID]
			if d.kinds[other.ID] == tableKindCodeTable {
				otherKey, _ = tableKey(other)
			}
			if len(otherKey) != 1 {
				continue
			}
			if k, _ := column(other, otherKey[0]); k.Type != c.Type {
				continue
			}
			if rank := nameMatch(c.Name, other, otherKey[0]); rank > 0 {
				candidates = append(candidates, candidate{other, otherKey[0], rank})
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank > candidates[j].rank })
		for _, cand := range candidates {
			drafted, err := d.refer(ctx, table, source, c.Name, cand.table, cand.key, interfaces.DRAFT_ORIGIN_NAME_MATCH, ids)
			if err != nil {
				return err
			}
			if drafted {
				break
			}
		}
	}
	return nil
}

// genericKeys are key names too common to say which table a field of that name refers to.
var genericKeys = map[string]bool{"id": true, "code": true, "key": true, "no": true, "uuid": true, "name": true}

// nameMatch ranks how a field's name points at another table's key, 0 when it does not:
// <table>_id, <table>_<key> or the key's own name rank above a role in front of the key
// (parent_lot_id for lot_id), and a longer matched suffix above a shorter one, so
// sub_account_id goes to sub_account before account. A generic key name (id, code) only
// matches with the table's name in front.
func nameMatch(field string, other *interfaces.VegaTable, key string) int {
	f, k, table := matchName(field), matchName(key), matchName(physicalName(other))
	switch {
	case f == table+"_id" || f == table+"_"+k || (f == k && !genericKeys[k]):
		return 1000
	case strings.HasSuffix(f, "_"+table+"_id"):
		return len(table) + 3
	case !genericKeys[k] && strings.HasSuffix(f, "_"+k):
		return len(k)
	}
	return 0
}

// refer drafts what a field referring to another table's key becomes: an enum when the other
// table is a code table, else a relation checked by full-table counts.
func (d *drafter) refer(ctx context.Context, table *interfaces.VegaTable, source *interfaces.DraftObjectType,
	field string, target *interfaces.VegaTable, key string, origin string, ids map[string]bool) (bool, error) {
	property, ok := d.fields[table.ID][field]
	if !ok {
		return false, nil
	}
	if valueType := d.valueTypes[target.ID]; valueType != nil {
		if p := findProperty(source, property); p != nil && p.Type == valueType.BaseType {
			p.ValueTypeID = valueType.ID
			valueType.UsedBy = append(valueType.UsedBy, interfaces.DraftPropertyRef{ObjectType: source.ID, Property: property})
			return true, nil
		}
		return false, nil
	}
	targetType := d.objectTypes[target.ID]
	if targetType == nil || len(targetType.PrimaryKeys) == 0 {
		return false, nil
	}
	targetKey, ok := d.fields[target.ID][key]
	if !ok {
		return false, nil
	}

	relation := &interfaces.DraftRelationType{
		ID:           uniqueName(idFrom(source.ID+"_to_"+targetType.ID, idPrefixRelationType), ids),
		Source:       source.ID,
		Target:       targetType.ID,
		Origin:       origin,
		Cardinality:  interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
		Mapping:      []interfaces.DraftFieldMapping{{SourceProperty: property, TargetProperty: targetKey}},
		Verification: "unconfirmed",
	}
	relation.Name = d.relationName(source, property, relation.ID)
	if origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY {
		relation.Verification = "confirmed"
	}
	if isUniqueField(table, field) {
		relation.Cardinality = interfaces.DRAFT_CARDINALITY_ONE_TO_ONE
	}

	if table.CatalogID != target.CatalogID {
		relation.Evidence.Reason = reasonCrossCatalog
	} else {
		rows, nulls, orphans, distinct, err := d.counter.joinStats(ctx, table, field, target, key)
		switch {
		case err != nil && ctx.Err() != nil:
			return false, ctx.Err()
		case err != nil:
			relation.Evidence.Reason = fmt.Sprintf("%s: %v", reasonQueryFailed, err)
		default:
			relation.Evidence = joinEvidence(rows, nulls, orphans)
			relation.Evidence.SourceDistinct = distinct
		}
	}

	rate, computed := relation.Evidence.HitRate, relation.Evidence.Computed
	switch {
	case !computed:
		// Not counted (another catalog, a failed query): proposed for a person to check, never dropped.
		relation.ProposedBecause = proposedNotCounted
	case origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY && rate < d.setting.SuggestHitRate:
		relation.ProposedBecause = proposedBelowSuggestRate
	case origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY:
	case rate < d.setting.SuggestHitRate:
		// Counted and mostly orphans: an inference the data refutes is not proposed.
		return false, nil
	case rate < d.setting.AdoptHitRate:
		relation.ProposedBecause = proposedBelowAdoptRate
	case relation.Evidence.SourceDistinct < d.setting.MinInferredDistinct:
		// A field of a few values hits any key by chance, so it is proposed, not adopted.
		relation.ProposedBecause = proposedFewValues
	}
	relation.Adopted = relation.ProposedBecause == ""
	d.items.RelationTypes = append(d.items.RelationTypes, relation)
	return true, nil
}

func joinEvidence(rows int64, nulls int64, orphans int64) interfaces.DraftJoinEvidence {
	evidence := interfaces.DraftJoinEvidence{Computed: true, Rows: rows, Nulls: nulls, Orphans: orphans}
	evidence.Hits = rows - nulls - orphans
	if referring := rows - nulls; referring > 0 {
		evidence.HitRate = float64(evidence.Hits) / float64(referring)
	}
	return evidence
}

// addLinkRelation drafts a link table as a many-to-many relation backed by it.
func (d *drafter) addLinkRelation(table *interfaces.VegaTable, ids map[string]bool) {
	ends := [2]*interfaces.DraftObjectType{}
	keys := [2]string{}
	for i, fk := range table.ForeignKeys {
		target := d.target(table, fk)
		if target == nil || d.objectTypes[target.ID] == nil || len(d.objectTypes[target.ID].PrimaryKeys) == 0 {
			return
		}
		ends[i] = d.objectTypes[target.ID]
		key, ok := d.fields[target.ID][fk.RefColumns[0]]
		if !ok {
			return
		}
		keys[i] = key
	}
	link := draftTable(table)
	relation := &interfaces.DraftRelationType{
		ID:            d.itemID(interfaces.ID_KIND_RELATION_TYPES, table.ID, physicalName(table), table.APIName, idPrefixRelationType, ids),
		Source:        ends[0].ID,
		Target:        ends[1].ID,
		Origin:        interfaces.DRAFT_ORIGIN_LINK_TABLE,
		Cardinality:   interfaces.DRAFT_CARDINALITY_MANY_TO_MANY,
		LinkTable:     &link,
		SourceMapping: []interfaces.DraftFieldMapping{{SourceProperty: keys[0], TargetProperty: table.ForeignKeys[0].Columns[0]}},
		TargetMapping: []interfaces.DraftFieldMapping{{SourceProperty: table.ForeignKeys[1].Columns[0], TargetProperty: keys[1]}},
		Adopted:       true,
		Verification:  "confirmed",
		Evidence:      interfaces.DraftJoinEvidence{Reason: reasonLinkTable},
	}
	relation.Name, relation.Aliases = d.tableName(table)
	d.items.RelationTypes = append(d.items.RelationTypes, relation)
}

func findProperty(objectType *interfaces.DraftObjectType, name string) *interfaces.DraftProperty {
	for _, p := range objectType.Properties {
		if p.Name == name {
			return p
		}
	}
	return nil
}
