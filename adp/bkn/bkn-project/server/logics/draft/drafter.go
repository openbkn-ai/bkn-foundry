// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"sort"

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

// drafter drafts an ontology from the structure of readable tables.
type drafter struct {
	setting common.DraftSetting
	counter counter

	kinds        map[string]string
	objectTypes  map[string]*interfaces.DraftObjectType // by table id
	fields       map[string]map[string]string           // table id -> field -> property name
	valueTypes   map[string]*interfaces.DraftValueType  // by code table id
	bySourceName map[string]*interfaces.VegaTable       // schema.name -> table
	items        interfaces.DraftItems
}

func newDrafter(setting common.DraftSetting, c counter) *drafter {
	return &drafter{
		setting:      setting,
		counter:      c,
		kinds:        map[string]string{},
		objectTypes:  map[string]*interfaces.DraftObjectType{},
		fields:       map[string]map[string]string{},
		valueTypes:   map[string]*interfaces.DraftValueType{},
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

func sourceName(schema string, name string) string {
	return schema + "." + name
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
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	for _, table := range tables {
		d.kinds[table.ID] = classify(table, d.setting)
		d.bySourceName[sourceName(table.SourceSchema, table.SourceName)] = table
	}

	typeIDs := map[string]bool{}
	for _, table := range tables {
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
	for _, table := range tables {
		if d.kinds[table.ID] == tableKindCodeTable {
			if err := d.addValueType(ctx, table, valueTypeIDs); err != nil {
				return d.items, err
			}
		}
	}

	relationIDs := map[string]bool{}
	for _, table := range tables {
		var err error
		switch d.kinds[table.ID] {
		case tableKindEntity:
			err = d.addRelations(ctx, table, tables, relationIDs)
		case tableKindLinkTable:
			d.addLinkRelation(table, relationIDs)
		}
		if err != nil {
			return d.items, err
		}
	}
	for _, valueType := range d.valueTypes {
		valueType.Adopted = len(valueType.UsedBy) > 0
	}
	return d.items, nil
}

func (d *drafter) addObjectType(table *interfaces.VegaTable, typeIDs map[string]bool) {
	objectType := &interfaces.DraftObjectType{
		ID:          uniqueName(apiName(table.Name), typeIDs),
		Name:        displayName(table.Description, table.Name),
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

	names := map[string]bool{}
	fields := map[string]string{}
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
		name := uniqueName(apiName(c.Name), names)
		fields[c.Name] = name
		objectType.Properties = append(objectType.Properties, &interfaces.DraftProperty{
			Name:               name,
			DisplayName:        displayName(c.Description, c.Name),
			Type:               bknType,
			Description:        c.Description,
			MappedField:        c.Name,
			SourceOfRecord:     interfaces.SOURCE_OF_RECORD_TABLE,
			MissingDescription: c.Description == "",
		})
	}

	objectType.Key = interfaces.DraftKeyEvidence{Source: source}
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
	objectType.DisplayKey = displayKey(table, fields, objectType.PrimaryKeys)

	d.objectTypes[table.ID] = objectType
	d.fields[table.ID] = fields
	d.items.ObjectTypes = append(d.items.ObjectTypes, objectType)
}

// displayKey is the first mapped name field, else the first key property.
func displayKey(table *interfaces.VegaTable, fields map[string]string, primaryKeys []string) string {
	for _, c := range table.Columns {
		if name, ok := fields[c.Name]; ok && isNameField(c) {
			return name
		}
	}
	if len(primaryKeys) > 0 {
		return primaryKeys[0]
	}
	return ""
}

// checkKey counts the key over the full table: a key with duplicates or nulls needs a person to
// decide the identity.
func (d *drafter) checkKey(ctx context.Context, table *interfaces.VegaTable) error {
	objectType := d.objectTypes[table.ID]
	key, _ := tableKey(table)
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
	valueType := &interfaces.DraftValueType{
		ID:        uniqueName(apiName(table.Name), ids),
		Name:      displayName(table.Description, table.Name),
		BaseType:  bknTypes[c.Type],
		Values:    values,
		CodeTable: draftTable(table),
		UsedBy:    []interfaces.DraftPropertyRef{},
	}
	d.valueTypes[table.ID] = valueType
	d.items.ValueTypes = append(d.items.ValueTypes, valueType)
	return nil
}

// target finds the table a foreign key refers to, in the same schema as the referring table.
func (d *drafter) target(source *interfaces.VegaTable, fk interfaces.VegaForeignKey) *interfaces.VegaTable {
	return d.bySourceName[sourceName(source.SourceSchema, fk.RefTable)]
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
		if err := d.refer(ctx, table, source, fk.Columns[0], target, fk.RefColumns[0],
			interfaces.DRAFT_ORIGIN_FOREIGN_KEY, ids); err != nil {
			return err
		}
	}

	key, _ := tableKey(table)
	inKey := map[string]bool{}
	for _, field := range key {
		inKey[field] = true
	}
	for _, c := range table.Columns {
		if linked[c.Name] || inKey[c.Name] {
			continue
		}
		for _, other := range tables {
			if other.ID == table.ID || d.kinds[other.ID] == tableKindLinkTable {
				continue
			}
			otherKey, _ := tableKey(other)
			if len(otherKey) != 1 || !nameMatches(c.Name, other, otherKey[0]) {
				continue
			}
			if k, _ := column(other, otherKey[0]); k.Type != c.Type {
				continue
			}
			if err := d.refer(ctx, table, source, c.Name, other, otherKey[0],
				interfaces.DRAFT_ORIGIN_NAME_MATCH, ids); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// genericKeys are key names too common to say which table a field of that name refers to.
var genericKeys = map[string]bool{"id": true, "code": true, "key": true, "no": true, "uuid": true, "name": true}

// nameMatches reports whether a field is named for another table's key: <table>_id,
// <table>_<key>, or the key's own name when that name is not a generic one.
func nameMatches(field string, other *interfaces.VegaTable, key string) bool {
	f, k, table := apiName(field), apiName(key), apiName(other.Name)
	return f == table+"_id" || f == table+"_"+k || (f == k && !genericKeys[k])
}

// refer drafts what a field referring to another table's key becomes: an enum when the other
// table is a code table, else a relation checked by full-table counts.
func (d *drafter) refer(ctx context.Context, table *interfaces.VegaTable, source *interfaces.DraftObjectType,
	field string, target *interfaces.VegaTable, key string, origin string, ids map[string]bool) error {
	property, ok := d.fields[table.ID][field]
	if !ok {
		return nil
	}
	if valueType := d.valueTypes[target.ID]; valueType != nil {
		if p := findProperty(source, property); p != nil && p.Type == valueType.BaseType {
			p.ValueTypeID = valueType.ID
			valueType.UsedBy = append(valueType.UsedBy, interfaces.DraftPropertyRef{ObjectType: source.ID, Property: property})
		}
		return nil
	}
	targetType := d.objectTypes[target.ID]
	if targetType == nil || len(targetType.PrimaryKeys) == 0 {
		return nil
	}
	targetKey, ok := d.fields[target.ID][key]
	if !ok {
		return nil
	}

	relation := &interfaces.DraftRelationType{
		ID:           uniqueName(apiName(source.ID+"_to_"+targetType.ID), ids),
		Source:       source.ID,
		Target:       targetType.ID,
		Origin:       origin,
		Cardinality:  interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
		Mapping:      []interfaces.DraftFieldMapping{{SourceProperty: property, TargetProperty: targetKey}},
		Verification: "unconfirmed",
	}
	relation.Name = relation.ID
	if origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY {
		relation.Verification = "confirmed"
	}
	if isUniqueField(table, field) {
		relation.Cardinality = interfaces.DRAFT_CARDINALITY_ONE_TO_ONE
	}

	if table.CatalogID != target.CatalogID {
		relation.Evidence.Reason = reasonCrossCatalog
	} else {
		rows, nulls, orphans, err := d.counter.joinStats(ctx, table, field, target, key)
		switch {
		case err != nil && ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			relation.Evidence.Reason = fmt.Sprintf("%s: %v", reasonQueryFailed, err)
		default:
			relation.Evidence = joinEvidence(rows, nulls, orphans)
		}
	}

	rate, computed := relation.Evidence.HitRate, relation.Evidence.Computed
	switch {
	case origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY:
		relation.Adopted = computed && rate >= d.setting.SuggestHitRate
	case !computed || rate < d.setting.SuggestHitRate:
		// An inferred relation without counts that back it is not proposed.
		return nil
	default:
		relation.Adopted = rate >= d.setting.AdoptHitRate
	}
	d.items.RelationTypes = append(d.items.RelationTypes, relation)
	return nil
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
		ID:            uniqueName(apiName(table.Name), ids),
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
	relation.Name = displayName(table.Description, table.Name)
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
