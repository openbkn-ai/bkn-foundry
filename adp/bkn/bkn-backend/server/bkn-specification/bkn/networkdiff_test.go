// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func objectType(id, name string, tags []string, props ...*DataProperty) *BknObjectType {
	return &BknObjectType{
		BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{Type: "object_type", ID: id, Name: name, Tags: tags},
		DataSource:               &ResourceInfo{Type: "resource", ID: "2017573348757762050", Name: "erp_material_bom"},
		DataProperties:           props,
		PrimaryKeys:              []string{"bom_material_code"},
	}
}

func prop(name, display, description string) *DataProperty {
	return &DataProperty{Name: name, DisplayName: display, Type: "string", Description: description, MappedField: name}
}

func networkWith(id, name string, ots ...*BknObjectType) *BknNetwork {
	return &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{Type: "knowledge_network", ID: id, Name: name},
		ObjectTypes:           ots,
	}
}

func changeByPath(t *testing.T, changes []FieldChange, path string) FieldChange {
	t.Helper()
	for _, c := range changes {
		if c.Path == path {
			return c
		}
	}
	t.Fatalf("no change at %q; got %+v", path, changes)
	return FieldChange{}
}

func TestDiffNetworkModels_ReportsFieldLevelChanges(t *testing.T) {
	base := networkWith("net-a", "供应链主网",
		objectType("bom", "产品BOM", []string{"供应链", "已审核"},
			prop("alt_part", "替代件", "主料为空，其他为√；"),
			prop("alt_method", "替代方式", "直接替代或者需要改造改制后替代"),
		))
	target := networkWith("net-a", "供应链主网",
		objectType("bom", "产品物料清单", []string{"供应链", "已废止"},
			prop("alt_part", "替代件", "主料为空，替代料标记为 √"),
			prop("batch_no", "批次号", "同一 BOM 版本下的投料批次"),
		))

	result := DiffNetworkModels(base, target, DiffOptions{})

	require.Len(t, result.Entries, 1)
	entry := result.Entries[0]
	assert.Equal(t, "object_type", entry.Type)
	assert.Equal(t, "bom", entry.ID)
	assert.Equal(t, DiffUpdate, entry.Action)
	assert.Equal(t, "id", entry.PairedBy)
	assert.Equal(t, ValueChange{Old: "产品BOM", New: "产品物料清单"}, entry.Name)

	name := changeByPath(t, entry.Changes, "name")
	assert.Equal(t, DiffUpdate, name.Kind)
	assert.Equal(t, "产品BOM", name.Old)

	tags := changeByPath(t, entry.Changes, "tags")
	assert.Equal(t, "供应链, 已审核", tags.Old)
	assert.Equal(t, "供应链, 已废止", tags.New)

	edited := changeByPath(t, entry.Changes, "data_properties[alt_part].description")
	assert.Equal(t, DiffUpdate, edited.Kind)
	assert.Equal(t, "主料为空，其他为√；", edited.Old)

	// An added or removed property is reported field by field, not as one rendered cell.
	assert.Equal(t, DiffCreate, changeByPath(t, entry.Changes, "data_properties[batch_no].display_name").Kind)
	assert.Equal(t, "批次号", changeByPath(t, entry.Changes, "data_properties[batch_no].display_name").New)
	assert.Equal(t, DiffDelete, changeByPath(t, entry.Changes, "data_properties[alt_method].display_name").Kind)
	assert.Equal(t, "替代方式", changeByPath(t, entry.Changes, "data_properties[alt_method].display_name").Old)

	assert.Equal(t, DiffSummary{Updated: 1}, result.Summary)
}

// Reordering a property table is not a change: the table is keyed by property name, and aligning
// by position would report every row below an insertion as modified.
func TestDiffNetworkModels_PropertyOrderIsNotAChange(t *testing.T) {
	a := prop("alt_part", "替代件", "主料为空")
	b := prop("batch_no", "批次号", "投料批次")

	base := networkWith("net-a", "供应链主网", objectType("bom", "产品BOM", nil, a, b))
	target := networkWith("net-a", "供应链主网", objectType("bom", "产品BOM", nil, b, a))

	result := DiffNetworkModels(base, target, DiffOptions{})
	assert.Empty(t, result.Entries)
	assert.Equal(t, 1, result.Summary.Unchanged)
}

func TestDiffNetworkModels_UnchangedDefinitionsStayOutOfEntries(t *testing.T) {
	ot := objectType("bom", "产品BOM", []string{"供应链"}, prop("alt_part", "替代件", "主料为空"))
	result := DiffNetworkModels(networkWith("net-a", "网", ot), networkWith("net-a", "网", ot), DiffOptions{})

	assert.Empty(t, result.Entries)
	assert.Equal(t, DiffSummary{Unchanged: 1}, result.Summary)
}

// Two networks always carry different names and ids. That difference is reported on its own so it
// never occupies one of the "modified" slots a reader scans for real schema change.
func TestDiffNetworkModels_NetworkDifferenceIsReportedSeparately(t *testing.T) {
	ot := objectType("bom", "产品BOM", nil, prop("alt_part", "替代件", "主料为空"))
	result := DiffNetworkModels(networkWith("net-a", "供应链主网", ot), networkWith("net-b", "供应链主网（试点）", ot), DiffOptions{})

	require.NotNil(t, result.Network)
	assert.Equal(t, DiffUpdate, result.Network.Action)
	assert.Equal(t, "net-a", result.Network.BaseID)
	assert.Equal(t, "net-b", result.Network.TargetID)
	assert.Empty(t, result.Entries)
	assert.Equal(t, DiffSummary{Unchanged: 1}, result.Summary, "the root file is not counted with the definitions")
}

func TestDiffNetworkModels_LineageReportsNoOverlap(t *testing.T) {
	base := networkWith("net-a", "供应链主网", objectType("bom", "产品BOM", nil))
	target := networkWith("net-b", "华东仓储网", objectType("0193-uuid", "产品BOM", nil))

	result := DiffNetworkModels(base, target, DiffOptions{})
	assert.False(t, result.Lineage.Related)
	assert.Equal(t, 0, result.Lineage.CommonIDs)
	assert.Equal(t, 2, result.Lineage.TotalIDs)
	assert.Equal(t, DiffSummary{Created: 1, Deleted: 1}, result.Summary)
}

func TestDiffNetworkModels_NameFallbackPairsAndKeepsBothIDs(t *testing.T) {
	base := networkWith("net-a", "供应链主网",
		objectType("supplier", "供应商", nil, prop("code", "编码", "供应商编码")))
	target := networkWith("net-b", "华东仓储网",
		objectType("0193-uuid", "供应商", nil, prop("code", "编码", "SRM 供应商编码")))

	off := DiffNetworkModels(base, target, DiffOptions{})
	assert.Equal(t, DiffSummary{Created: 1, Deleted: 1}, off.Summary, "name fallback must be opt-in")

	on := DiffNetworkModels(base, target, DiffOptions{FallbackByName: true})
	require.Len(t, on.Entries, 1)
	entry := on.Entries[0]
	assert.Equal(t, DiffUpdate, entry.Action)
	assert.Equal(t, "name", entry.PairedBy)
	assert.Equal(t, "supplier", entry.BaseID)
	assert.Equal(t, "0193-uuid", entry.TargetID)
	assert.Equal(t, DiffUpdate, changeByPath(t, entry.Changes, "data_properties[code].description").Kind)
}

// Two candidates with the same name cannot be told apart, and picking one would invent a
// modification that never happened.
func TestDiffNetworkModels_NameFallbackSkipsAmbiguousNames(t *testing.T) {
	base := networkWith("net-a", "网", objectType("supplier", "供应商", nil))
	target := networkWith("net-b", "网",
		objectType("uuid-1", "供应商", nil),
		objectType("uuid-2", "供应商", nil))

	result := DiffNetworkModels(base, target, DiffOptions{FallbackByName: true})
	assert.Equal(t, DiffSummary{Created: 2, Deleted: 1}, result.Summary)
	for _, entry := range result.Entries {
		assert.NotEqual(t, DiffUpdate, entry.Action)
	}
}

func TestDiffNetworkModels_DetectsDataSourceRebinding(t *testing.T) {
	base := networkWith("net-a", "网", objectType("supplier", "供应商", nil))
	rebound := objectType("supplier", "供应商", nil)
	rebound.DataSource = &ResourceInfo{Type: "resource", ID: "2019884411203391488", Name: "srm_vendor_master"}
	target := networkWith("net-a", "网", rebound)

	result := DiffNetworkModels(base, target, DiffOptions{})
	require.Len(t, result.Entries, 1)
	assert.Equal(t, "2017573348757762050", changeByPath(t, result.Entries[0].Changes, "data_source.id").Old)
	assert.Equal(t, "srm_vendor_master", changeByPath(t, result.Entries[0].Changes, "data_source.name").New)
}

func TestSnakeCase(t *testing.T) {
	assert.Equal(t, "data_properties", snakeCase("DataProperties"))
	assert.Equal(t, "display_key", snakeCase("DisplayKey"))
	assert.Equal(t, "id", snakeCase("ID"))
	assert.Equal(t, "primary_keys", snakeCase("PrimaryKeys"))
	assert.Equal(t, "mapped_field", snakeCase("MappedField"))
	assert.Equal(t, "business_domain", snakeCase("BusinessDomain"))
}

// A definition that exists on one side only is still worth reading: the panel should show what is
// being added or removed, not an empty state.
func TestDiffNetworkModels_CreatedDefinitionCarriesItsContent(t *testing.T) {
	base := networkWith("net-a", "网")
	target := networkWith("net-a", "网",
		objectType("batch", "批次", []string{"供应链"}, prop("batch_no", "批次号", "投料批次")))

	result := DiffNetworkModels(base, target, DiffOptions{})
	require.Len(t, result.Entries, 1)
	entry := result.Entries[0]
	assert.Equal(t, DiffCreate, entry.Action)
	require.NotEmpty(t, entry.Changes, "a created definition must carry its content")

	name := changeByPath(t, entry.Changes, "name")
	assert.Equal(t, DiffCreate, name.Kind)
	assert.Empty(t, name.Old)
	assert.Equal(t, "批次", name.New)

	// The property is reported field by field rather than as one rendered blob.
	assert.Equal(t, "批次号", changeByPath(t, entry.Changes, "data_properties[batch_no].display_name").New)
	assert.Equal(t, "投料批次", changeByPath(t, entry.Changes, "data_properties[batch_no].description").New)
}

func TestDiffNetworkModels_DeletedDefinitionCarriesItsContent(t *testing.T) {
	base := networkWith("net-a", "网",
		objectType("batch", "批次", nil, prop("batch_no", "批次号", "投料批次")))
	target := networkWith("net-a", "网")

	result := DiffNetworkModels(base, target, DiffOptions{})
	require.Len(t, result.Entries, 1)
	require.NotEmpty(t, result.Entries[0].Changes)
	removed := changeByPath(t, result.Entries[0].Changes, "data_properties[batch_no].display_name")
	assert.Equal(t, DiffDelete, removed.Kind)
	assert.Equal(t, "批次号", removed.Old)
	assert.Empty(t, removed.New)
}

// An added property is reported one field per row. The blob it replaced —
// "name=abbr display_name=abbr description=..." in a single cell — had to be decoded before a
// reader could see what was added.
func TestDiffNetworkModels_AddedPropertyIsReportedFieldByField(t *testing.T) {
	base := networkWith("net-a", "网", objectType("bom", "产品BOM", nil))
	target := networkWith("net-a", "网", objectType("bom", "产品BOM", nil,
		prop("abbr", "缩写", "物理列")))

	result := DiffNetworkModels(base, target, DiffOptions{})
	require.Len(t, result.Entries, 1)
	changes := result.Entries[0].Changes
	assert.Equal(t, "缩写", changeByPath(t, changes, "data_properties[abbr].display_name").New)
	assert.Equal(t, "物理列", changeByPath(t, changes, "data_properties[abbr].description").New)
	for _, change := range changes {
		assert.NotContains(t, change.New, "display_name=", "no rendered struct blob should survive")
	}
}
