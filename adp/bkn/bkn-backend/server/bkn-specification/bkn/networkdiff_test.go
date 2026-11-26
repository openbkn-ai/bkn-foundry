// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"encoding/json"
	"fmt"
	"strings"
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

// A reader listing the whole model beside the differences needs the identical definitions too.
// They are counted either way; only their presence in the list changes.
func TestDiffNetworkModels_IncludeUnchangedListsIdenticalDefinitions(t *testing.T) {
	shared := objectType("bom", "产品BOM", nil, prop("alt_part", "替代件", "主料为空"))
	base := networkWith("net-a", "网", shared)
	target := networkWith("net-a", "网", shared)

	without := DiffNetworkModels(base, target, DiffOptions{})
	assert.Empty(t, without.Entries)
	assert.Equal(t, 1, without.Summary.Unchanged)

	with := DiffNetworkModels(base, target, DiffOptions{IncludeUnchanged: true})
	require.Len(t, with.Entries, 1)
	assert.Equal(t, DiffSkip, with.Entries[0].Action)
	assert.Equal(t, "bom", with.Entries[0].ID)
	// An identical entry is there to be listed, not read: it carries no field rows.
	assert.Empty(t, with.Entries[0].Changes)
	assert.Equal(t, 1, with.Summary.Unchanged, "the count does not change with the option")
}

// metricRecord is an archived object metric record as an export or a version snapshot carries it:
// the definition wrapped in where and when it is stored.
func metricRecord(description, branch, revision string, updateTime int64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"definition": {"id": "m_net_paid", "version": 1, "code": "net_paid", "name": "实付净额",
			"description": %q, "owner_object_type_id": "order", "metric_type": "atomic",
			"lifecycle": {"status": "published"}},
		"kn_id": "net-a", "branch": %q, "revision": %q,
		"creator": {"id": "u1", "type": "user", "name": "alice"},
		"updater": {"id": "u1", "type": "user", "name": "alice"},
		"create_time": 1763000000000, "update_time": %d}`, description, branch, revision, updateTime))
}

// A version and the draft store the same metric under different branches, and a re-save stamps a
// new revision and update time without touching the definition. None of that is a change to the
// metric: comparing a version with its draft must not report every metric as modified.
func TestDiffNetworkModels_MetricStorageMetadataIsNotAChange(t *testing.T) {
	base := networkWith("net-a", "网")
	base.ObjectMetrics = []json.RawMessage{metricRecord("扣除退款后的实付金额", "v2", "rev-1", 1763000000000)}
	target := networkWith("net-a", "网")
	target.ObjectMetrics = []json.RawMessage{metricRecord("扣除退款后的实付金额", "draft", "rev-2", 1763999999999)}

	result := DiffNetworkModels(base, target, DiffOptions{})

	assert.Empty(t, result.Entries)
	assert.Equal(t, DiffSummary{Unchanged: 1}, result.Summary)
}

func TestDiffNetworkModels_MetricDefinitionChangeIgnoresStorageMetadata(t *testing.T) {
	base := networkWith("net-a", "网")
	base.ObjectMetrics = []json.RawMessage{metricRecord("扣除退款后的实付金额", "v2", "rev-1", 1763000000000)}
	target := networkWith("net-a", "网")
	target.ObjectMetrics = []json.RawMessage{metricRecord("扣除退款和运费后的实付金额", "draft", "rev-2", 1763999999999)}

	result := DiffNetworkModels(base, target, DiffOptions{})

	require.Len(t, result.Entries, 1)
	entry := result.Entries[0]
	assert.Equal(t, "object_metric_v1", entry.Type)
	assert.Equal(t, "m_net_paid@1.json", entry.ID)
	assert.Equal(t, DiffUpdate, entry.Action)
	require.Len(t, entry.Changes, 1, "only the definition edit is a change; got %+v", entry.Changes)
	description := changeByPath(t, entry.Changes, "definition.description")
	assert.Equal(t, "扣除退款后的实付金额", description.Old)
	assert.Equal(t, "扣除退款和运费后的实付金额", description.New)
}

// A metric present on one side only shows its definition, the way a created object type does, and
// not the storage bookkeeping of the branch it came from.
func TestDiffNetworkModels_CreatedMetricOmitsStorageMetadata(t *testing.T) {
	base := networkWith("net-a", "网")
	target := networkWith("net-a", "网")
	target.ObjectMetrics = []json.RawMessage{metricRecord("扣除退款后的实付金额", "draft", "rev-2", 1763999999999)}

	result := DiffNetworkModels(base, target, DiffOptions{})

	require.Len(t, result.Entries, 1)
	entry := result.Entries[0]
	assert.Equal(t, DiffCreate, entry.Action)
	require.NotEmpty(t, entry.Changes)
	var rendered []string
	for _, change := range entry.Changes {
		rendered = append(rendered, change.Path+" "+change.New)
	}
	all := strings.Join(rendered, "\n")
	assert.Contains(t, all, "扣除退款后的实付金额")
	for _, field := range []string{"kn_id", "branch", "revision", "creator", "updater", "create_time", "update_time"} {
		assert.NotContains(t, all, field+"=", "storage field %q leaked into a created metric", field)
		assert.NotContains(t, all, field+" ", "storage field %q leaked into a created metric", field)
	}
}

// A metric is carried as a JSON record, not a Go struct. Present on one side only, it is still
// listed field by field like a created object type, rather than as one row holding the record.
func TestDiffNetworkModels_DeletedMetricIsReportedFieldByField(t *testing.T) {
	base := networkWith("net-a", "网")
	base.ObjectMetrics = []json.RawMessage{metricRecord("扣除退款后的实付金额", "v2", "rev-1", 1763000000000)}
	target := networkWith("net-a", "网")

	result := DiffNetworkModels(base, target, DiffOptions{})

	require.Len(t, result.Entries, 1)
	entry := result.Entries[0]
	assert.Equal(t, DiffDelete, entry.Action)
	description := changeByPath(t, entry.Changes, "definition.description")
	assert.Equal(t, DiffDelete, description.Kind)
	assert.Equal(t, "扣除退款后的实付金额", description.Old)
	assert.Equal(t, "published", changeByPath(t, entry.Changes, "definition.lifecycle.status").Old)
	assert.Equal(t, "1", changeByPath(t, entry.Changes, "definition.version").Old)
	for _, change := range entry.Changes {
		assert.NotEqual(t, ".", change.Path, "the record must not collapse into one row")
	}
}
