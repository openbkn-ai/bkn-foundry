// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The directories of a BKN package that confirming a draft writes; the rest of a branch is not
// the draft's.
var compiledDirs = []string{"object_types/", "relation_types/", "value_types/"}

// compiledDraft is a draft written as BKN definitions.
type compiledDraft struct {
	// Files are the definition files, keyed by their path in the package.
	Files map[string][]byte
	// SkippedValueTypes are adopted value types BKN 3.0 would refuse: a base type it has no value
	// type for, or a name another adopted value type took first. Their properties keep their base
	// type, and confirming says which they are.
	SkippedValueTypes []string
	// IDKeys name the items written whose ids may be registered: a confirmation locks them, so
	// what is on the branch keeps its ids.
	IDKeys []interfaces.IDKey
}

// compileDraft writes the adopted items of a draft as the definition files of a BKN 3.0 package,
// keyed by their path in the package: the object types with their tables as data sources, the
// relation types whose ends are adopted, and the value types an adopted property can reference.
// Every lineage is kept in 3.0, the only specification with a place for value types.
//
// The definitions go through the BKN specification's own writer, which has no public way to
// write a single 3.0 definition: a package holding only them is written and its definition files
// kept. Its network.bkn is not, since a branch has its own.
// addIDKey names an item written from the table sourceKey names; an item from no table has none.
func (c *compiledDraft) addIDKey(kind string, sourceKey string) {
	if sourceKey != "" {
		c.IDKeys = append(c.IDKeys, interfaces.IDKey{Kind: kind, SourceKey: sourceKey})
	}
}

func compileDraft(items *interfaces.DraftItems) (*compiledDraft, error) {
	network := &bkn.BknNetwork{
		BknNetworkFrontmatter: bkn.BknNetworkFrontmatter{
			Type:    "knowledge_network",
			ID:      "draft",
			Name:    "draft",
			BknSpec: bkn.BknSpecV3,
		},
	}

	result := &compiledDraft{Files: map[string][]byte{}, SkippedValueTypes: []string{}}
	valueTypes, names := map[string]bool{}, map[string]bool{}
	for _, vt := range items.ValueTypes {
		if !vt.Adopted {
			continue
		}
		compiled := compileValueType(vt)
		// One value type BKN 3.0 refuses would fail the whole package, so it is left out alone.
		if bkn.ValidateValueType(compiled) != nil || names[compiled.Name] {
			result.SkippedValueTypes = append(result.SkippedValueTypes, vt.ID)
			continue
		}
		valueTypes[vt.ID], names[compiled.Name] = true, true
		network.ValueTypes = append(network.ValueTypes, compiled)
		result.addIDKey(interfaces.ID_KIND_VALUE_TYPES, vt.CodeTable.ResourceID)
	}
	objectTypes := map[string]bool{}
	for _, ot := range items.ObjectTypes {
		if !ot.Adopted {
			continue
		}
		if ot.Table.ResourceID == "" {
			return nil, fmt.Errorf("object type %q has no table", ot.ID)
		}
		objectTypes[ot.ID] = true
		network.ObjectTypes = append(network.ObjectTypes, compileObjectType(ot, valueTypes))
		result.addIDKey(interfaces.ID_KIND_OBJECT_TYPES, ot.Table.ResourceID)
		for _, p := range ot.Properties {
			if p.MappedField != "" {
				result.addIDKey(interfaces.ID_KIND_PROPERTIES, propertySourceKey(ot.Table.ResourceID, p.MappedField))
			}
		}
	}
	for _, rt := range items.RelationTypes {
		// A relation adopted while an end is not is a pending item a person settles first; it
		// would not be read as a relation of this network either.
		if rt.Adopted && objectTypes[rt.Source] && objectTypes[rt.Target] {
			compiled, err := compileRelationType(rt)
			if err != nil {
				return nil, err
			}
			network.RelationTypes = append(network.RelationTypes, compiled)
			if rt.LinkTable != nil {
				result.addIDKey(interfaces.ID_KIND_RELATION_TYPES, rt.LinkTable.ResourceID)
			}
		}
	}

	var archive bytes.Buffer
	if err := bkn.WriteNetworkToTar(network, &archive); err != nil {
		return nil, fmt.Errorf("write definitions: %w", err)
	}
	written, err := common.ReadBKNTar(archive.Bytes())
	if err != nil {
		return nil, fmt.Errorf("read written definitions: %w", err)
	}
	for path, data := range written {
		for _, dir := range compiledDirs {
			if strings.HasPrefix(path, dir) {
				result.Files[path] = data
			}
		}
	}
	return result, nil
}

func compileObjectType(ot *interfaces.DraftObjectType, valueTypes map[string]bool) *bkn.BknObjectType {
	compiled := &bkn.BknObjectType{
		BknObjectTypeFrontmatter: bkn.BknObjectTypeFrontmatter{
			Type: "object_type", ID: ot.ID, Name: ot.Name, Aliases: ot.Aliases,
		},
		Description: ot.Description,
		DataSource: &bkn.ResourceInfo{
			Type: bkn.DATA_SOURCE_TYPE_RESOURCE,
			ID:   ot.Table.ResourceID,
			Name: ot.Table.Name,
		},
		PrimaryKeys: ot.PrimaryKeys,
		DisplayKey:  ot.DisplayKey,
	}
	for _, p := range ot.Properties {
		property := &bkn.DataProperty{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Aliases:     p.Aliases,
			Type:        p.Type,
			Description: p.Description,
			MappedField: p.MappedField,
		}
		// A property keeps its base type when the value type it referenced is not adopted.
		if p.ValueTypeID != "" && valueTypes[p.ValueTypeID] {
			property.Constraints = &bkn.PropertyConstraints{ValueType: p.ValueTypeID}
		}
		compiled.DataProperties = append(compiled.DataProperties, property)
	}
	return compiled
}

func compileRelationType(rt *interfaces.DraftRelationType) (*bkn.BknRelationType, error) {
	compiled := &bkn.BknRelationType{
		BknRelationTypeFrontmatter: bkn.BknRelationTypeFrontmatter{
			Type:         "relation_type",
			ID:           rt.ID,
			Name:         rt.Name,
			Aliases:      rt.Aliases,
			Verification: rt.Verification,
		},
		Description: rt.Description,
		Endpoint: bkn.Endpoint{
			Source:      rt.Source,
			Target:      rt.Target,
			Cardinality: rt.Cardinality,
		},
	}
	if rt.LinkTable == nil {
		compiled.Endpoint.Type = "direct"
		compiled.MappingRules = bkn.DirectMappingRule(mappingRules(rt.Mapping))
		return compiled, nil
	}
	if rt.LinkTable.ResourceID == "" {
		return nil, fmt.Errorf("relation type %q has no link table", rt.ID)
	}
	compiled.Endpoint.Type = "indirect"
	// The writer reads an indirect mapping only by pointer, as the parser writes it.
	compiled.MappingRules = &bkn.InDirectMappingRule{
		BackingDataSource: &bkn.ResourceInfo{
			Type: bkn.DATA_SOURCE_TYPE_RESOURCE,
			ID:   rt.LinkTable.ResourceID,
			Name: rt.LinkTable.Name,
		},
		SourceMappingRules: mappingRules(rt.SourceMapping),
		TargetMappingRules: mappingRules(rt.TargetMapping),
	}
	return compiled, nil
}

func mappingRules(mapping []interfaces.DraftFieldMapping) []bkn.MappingRule {
	rules := make([]bkn.MappingRule, 0, len(mapping))
	for _, m := range mapping {
		rules = append(rules, bkn.MappingRule{SourceProperty: m.SourceProperty, TargetProperty: m.TargetProperty})
	}
	return rules
}

func compileValueType(vt *interfaces.DraftValueType) *bkn.BknValueType {
	compiled := &bkn.BknValueType{
		BknValueTypeFrontmatter: bkn.BknValueTypeFrontmatter{Type: "value_type", ID: vt.ID, Name: vt.Name},
		Description:             vt.Description,
		BaseType:                vt.BaseType,
	}
	for _, v := range vt.Values {
		// A code without a name in its code table stands for itself.
		display := v.Display
		if display == "" {
			display = v.Value
		}
		compiled.Enum = append(compiled.Enum, &bkn.ValueTypeEnumItem{Value: v.Value, Display: display})
	}
	return compiled
}
