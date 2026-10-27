// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package merge

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	networkPath  = "network.bkn"
	partPath     = "object_types/part.bkn"
	supplierPath = "object_types/supplier.bkn"
	suppliesPath = "relation_types/supplier_supplies_part.bkn"
	skillPath    = "SKILL.md"
	metricPath   = "object_metrics/stock_level@1.json"
)

// The fixtures keep the layout of a bkn-backend export, with a few extra blank lines so that a
// file the merge serialized again cannot pass for one it copied.
const networkText = `---
type: knowledge_network
id: supply
name: Supply chain
tags: [demo]
---

# Supply chain

Parts and the suppliers that deliver them.


## Network Overview
`

const relationText = `---
type: relation_type
id: supplier_supplies_part
name: Supplier supplies part
tags: [demo]
---

## RelationType: Supplier supplies part

A supplier delivers a part.


### Endpoint

| Source | Target | Type |
|--------|--------|------|
| supplier | part | direct |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| supplier_id | supplier_id |
`

func objectType(id, name, desc string, rows ...string) string {
	return fmt.Sprintf(`---
type: object_type
id: %s
name: %s
tags: [demo]
---

## ObjectType: %s

%s


### Data Source

| Type | ID | Name |
|------|----|------|
| resource | view_%s | %s_view |

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
%s

### Logic Properties


### Keys

Primary Keys: id
Display Key: id
Incremental Key:
`, id, name, name, desc, id, id, strings.Join(rows, "\n"))
}

func row(name, typ, desc string) string {
	return fmt.Sprintf("| %s | %s | %s | %s | %s |", name, name, typ, desc, name)
}

var (
	partBase = objectType("part", "Part", "A part that can be ordered.",
		row("id", "string", "Part number"), row("weight", "float", "Weight in kg"))
	supplierBase = objectType("supplier", "Supplier", "A company that delivers parts.",
		row("id", "string", "Supplier id"))
)

func baseSet() map[string][]byte {
	return map[string][]byte{
		networkPath:  []byte(networkText),
		partPath:     []byte(partBase),
		supplierPath: []byte(supplierBase),
		suppliesPath: []byte(relationText),
		skillPath:    []byte("# Supply chain skill\n"),
		metricPath:   []byte(`{"definition":{"id":"stock_level","version":1,"name":"Stock level"}}`),
	}
}

// with copies set and applies changes; a nil value deletes the path.
func with(set map[string][]byte, changes map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for p, data := range set {
		out[p] = data
	}
	for p, text := range changes {
		if text == "" {
			delete(out, p)
			continue
		}
		out[p] = []byte(text)
	}
	return out
}

func loadMerged(files map[string][]byte) *bkn.BknNetwork {
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	net, err := bkn.LoadNetworkWithFS(fsys, ".")
	So(err, ShouldBeNil)
	return net
}

func findObjectType(net *bkn.BknNetwork, id string) *bkn.BknObjectType {
	for _, ot := range net.ObjectTypes {
		if ot.ID == id {
			return ot
		}
	}
	return nil
}

func property(ot *bkn.BknObjectType, name string) *bkn.DataProperty {
	for _, p := range ot.DataProperties {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func Test_Merge_Definitions(t *testing.T) {
	Convey("Test Merge of whole definitions\n", t, func() {
		base := baseSet()
		partEdited := objectType("part", "Part", "A part that can be ordered and stocked.",
			row("id", "string", "Part number"), row("weight", "float", "Weight in kg"))
		partOther := objectType("part", "Part", "A part.",
			row("id", "string", "Part number"), row("weight", "float", "Weight in kg"))
		supplierEdited := objectType("supplier", "Supplier", "A company that delivers parts on time.",
			row("id", "string", "Supplier id"))

		cases := []struct {
			name      string
			ours      map[string]string
			theirs    map[string]string
			wantFiles map[string]string // path -> expected text; "" means absent
			wantSum   interfaces.MergeSummary
			conflicts []interfaces.MergeConflict
		}{
			{
				name:      "A change on one side is taken as it is",
				theirs:    map[string]string{partPath: partEdited},
				wantFiles: map[string]string{partPath: partEdited},
				wantSum:   interfaces.MergeSummary{Unchanged: 5, Theirs: 1},
			},
			{
				name:      "The same change on both sides is taken once",
				ours:      map[string]string{partPath: partEdited},
				theirs:    map[string]string{partPath: partEdited},
				wantFiles: map[string]string{partPath: partEdited},
				wantSum:   interfaces.MergeSummary{Unchanged: 5, Ours: 1},
			},
			{
				name:   "A definition added on one side is added",
				ours:   map[string]string{supplierPath: supplierEdited},
				theirs: map[string]string{"object_types/warehouse.bkn": objectType("warehouse", "Warehouse", "Stores parts.", row("id", "string", "Warehouse id"))},
				wantFiles: map[string]string{
					supplierPath:                 supplierEdited,
					"object_types/warehouse.bkn": objectType("warehouse", "Warehouse", "Stores parts.", row("id", "string", "Warehouse id")),
				},
				wantSum: interfaces.MergeSummary{Unchanged: 5, Ours: 1, Theirs: 1},
			},
			{
				name:      "A definition deleted on one side and unchanged on the other is deleted",
				theirs:    map[string]string{supplierPath: ""},
				wantFiles: map[string]string{supplierPath: ""},
				wantSum:   interfaces.MergeSummary{Unchanged: 5, Deleted: 1},
			},
			{
				name:      "Deleted by ours and changed by theirs conflicts and stays deleted",
				ours:      map[string]string{supplierPath: ""},
				theirs:    map[string]string{supplierPath: supplierEdited},
				wantFiles: map[string]string{supplierPath: ""},
				wantSum:   interfaces.MergeSummary{Unchanged: 5, Conflicted: 1},
				conflicts: []interfaces.MergeConflict{{Type: "object_type", ID: "supplier", File: supplierPath,
					Reason: interfaces.MERGE_CONFLICT_DELETED_BY_OURS}},
			},
			{
				name:      "Changed by ours and deleted by theirs conflicts and keeps ours",
				ours:      map[string]string{supplierPath: supplierEdited},
				theirs:    map[string]string{supplierPath: ""},
				wantFiles: map[string]string{supplierPath: supplierEdited},
				wantSum:   interfaces.MergeSummary{Unchanged: 5, Conflicted: 1},
				conflicts: []interfaces.MergeConflict{{Type: "object_type", ID: "supplier", File: supplierPath,
					Reason: interfaces.MERGE_CONFLICT_DELETED_BY_THEIRS}},
			},
			{
				name:      "Different descriptions on both sides conflict on the field and keep ours",
				ours:      map[string]string{partPath: partEdited},
				theirs:    map[string]string{partPath: partOther},
				wantSum:   interfaces.MergeSummary{Unchanged: 5, Conflicted: 1},
				conflicts: []interfaces.MergeConflict{{Type: "object_type", ID: "part", File: partPath, Reason: interfaces.MERGE_CONFLICT_BOTH_CHANGED, Path: "description", Base: "A part that can be ordered.", Ours: "A part that can be ordered and stocked.", Theirs: "A part."}},
			},
		}

		for _, c := range cases {
			Convey(c.name+"\n", func() {
				result, err := Merge(base, with(base, c.ours), with(base, c.theirs))

				So(err, ShouldBeNil)
				So(result.Summary, ShouldResemble, c.wantSum)
				if c.conflicts == nil {
					c.conflicts = []interfaces.MergeConflict{}
				}
				So(result.Conflicts, ShouldResemble, c.conflicts)
				So(result.HasConflicts(), ShouldEqual, len(c.conflicts) > 0)
				for p, text := range c.wantFiles {
					if text == "" {
						So(result.Files, ShouldNotContainKey, p)
						continue
					}
					So(string(result.Files[p]), ShouldEqual, text)
				}
				// Whatever nobody touched is copied byte for byte.
				for p, data := range base {
					_, inOurs := c.ours[p]
					_, inTheirs := c.theirs[p]
					if !inOurs && !inTheirs {
						So(result.Files[p], ShouldResemble, data)
					}
				}
				loadMerged(result.Files)
			})
		}

		Convey("The same id added on both sides merges field by field\n", func() {
			ours := with(base, map[string]string{"object_types/warehouse.bkn": objectType("warehouse", "Warehouse", "Stores parts.",
				row("id", "string", "Warehouse id"), row("city", "string", "City"))})
			theirs := with(base, map[string]string{"object_types/warehouse.bkn": objectType("warehouse", "Warehouse", "Stores parts.",
				row("id", "string", "Warehouse id"), row("capacity", "integer", "Capacity in pallets"))})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
			So(result.Summary.Merged, ShouldEqual, 1)
			wh := findObjectType(loadMerged(result.Files), "warehouse")
			So(wh, ShouldNotBeNil)
			So(property(wh, "city"), ShouldNotBeNil)
			So(property(wh, "capacity"), ShouldNotBeNil)
		})

		Convey("The same id added on both sides with different values conflicts on the field\n", func() {
			ours := with(base, map[string]string{"object_types/warehouse.bkn": objectType("warehouse", "Warehouse", "Stores parts.", row("id", "string", "Warehouse id"))})
			theirs := with(base, map[string]string{"object_types/warehouse.bkn": objectType("warehouse", "Depot", "Stores parts.", row("id", "string", "Warehouse id"))})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldResemble, []interfaces.MergeConflict{{Type: "object_type", ID: "warehouse",
				File: "object_types/warehouse.bkn", Reason: interfaces.MERGE_CONFLICT_BOTH_CHANGED, Path: "name", Ours: "Warehouse", Theirs: "Depot"}})
		})
	})
}

func Test_Merge_Fields(t *testing.T) {
	Convey("Test Merge of definitions both sides changed\n", t, func() {
		base := baseSet()

		Convey("Different properties of one object type merge cleanly and the file reads back\n", func() {
			ours := with(base, map[string]string{partPath: objectType("part", "Part", "A part that can be ordered.",
				row("id", "string", "Part number"), row("weight", "float", "Gross weight in kg"))})
			theirs := with(base, map[string]string{partPath: objectType("part", "Part", "A part that can be ordered.",
				row("id", "string", "Part number"), row("weight", "float", "Weight in kg"), row("color", "string", "Color"))})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 5, Merged: 1})
			So(string(result.Files[partPath]), ShouldNotEqual, string(ours[partPath]))
			So(string(result.Files[partPath]), ShouldNotEqual, string(theirs[partPath]))

			part := findObjectType(loadMerged(result.Files), "part")
			So(part, ShouldNotBeNil)
			So(property(part, "weight").Description, ShouldEqual, "Gross weight in kg")
			So(property(part, "color").Description, ShouldEqual, "Color")
			So(part.Description, ShouldEqual, "A part that can be ordered.")
			So(part.PrimaryKeys, ShouldResemble, []string{"id"})

			// Untouched definitions and files keep their bytes.
			for _, p := range []string{networkPath, supplierPath, suppliesPath, skillPath, metricPath} {
				So(result.Files[p], ShouldResemble, base[p])
			}
		})

		Convey("The same property changed differently is one conflict on that property\n", func() {
			ours := with(base, map[string]string{partPath: objectType("part", "Part", "A part that can be ordered.",
				row("id", "string", "Part number"), row("weight", "float", "Gross weight in kg"), row("color", "string", "Color"))})
			theirs := with(base, map[string]string{partPath: objectType("part", "Part", "A part that can be ordered.",
				row("id", "string", "Part number"), row("weight", "float", "Net weight in kg"))})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldResemble, []interfaces.MergeConflict{{Type: "object_type", ID: "part", File: partPath,
				Reason: interfaces.MERGE_CONFLICT_BOTH_CHANGED, Path: "data_properties[weight].description",
				Base: "Weight in kg", Ours: "Gross weight in kg", Theirs: "Net weight in kg"}})
			So(result.Summary.Conflicted, ShouldEqual, 1)
			part := findObjectType(loadMerged(result.Files), "part")
			So(property(part, "weight").Description, ShouldEqual, "Gross weight in kg")
			So(property(part, "color"), ShouldNotBeNil)
		})

		Convey("A relation type keeps its mapping rules through a field merge\n", func() {
			ours := with(base, map[string]string{suppliesPath: strings.Replace(relationText,
				"A supplier delivers a part.", "A supplier delivers a part under contract.", 1)})
			theirs := with(base, map[string]string{suppliesPath: relationText + "| supplier_region | region |\n"})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
			net := loadMerged(result.Files)
			So(net.RelationTypes, ShouldHaveLength, 1)
			rel := net.RelationTypes[0]
			So(rel.Description, ShouldEqual, "A supplier delivers a part under contract.")
			So(rel.MappingRules, ShouldResemble, bkn.DirectMappingRule{
				{SourceProperty: "supplier_id", TargetProperty: "supplier_id"},
				{SourceProperty: "supplier_region", TargetProperty: "region"},
			})
		})

		Convey("A network header changed on both sides merges by field\n", func() {
			ours := with(base, map[string]string{networkPath: strings.Replace(networkText, "tags: [demo]", "tags: [demo, parts]", 1)})
			theirs := with(base, map[string]string{networkPath: strings.Replace(networkText,
				"Parts and the suppliers that deliver them.", "Parts, suppliers and stock.", 1)})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
			net := loadMerged(result.Files)
			So(net.Tags, ShouldResemble, []string{"demo", "parts"})
			So(net.Description, ShouldEqual, "Parts, suppliers and stock.")
			So(net.ObjectTypes, ShouldHaveLength, 2)
		})
	})
}

func Test_Merge_OpaqueFiles(t *testing.T) {
	Convey("Test Merge of files that are not definitions\n", t, func() {
		base := baseSet()
		metric2 := "object_metrics/stock_level@2.json"

		Convey("Files merge by path\n", func() {
			ours := with(base, map[string]string{skillPath: "# Supply chain skill, revised\n"})
			theirs := with(base, map[string]string{metric2: `{"definition":{"id":"stock_level","version":2,"name":"Stock level"}}`})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
			So(result.Files[skillPath], ShouldResemble, ours[skillPath])
			So(result.Files[metric2], ShouldResemble, theirs[metric2])
			So(result.Files[metricPath], ShouldResemble, base[metricPath])
		})

		Convey("A file changed differently on both sides conflicts and keeps ours\n", func() {
			ours := with(base, map[string]string{metricPath: `{"definition":{"id":"stock_level","version":1,"name":"Stock"}}`})
			theirs := with(base, map[string]string{metricPath: `{"definition":{"id":"stock_level","version":1,"name":"Stock on hand"}}`})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldResemble, []interfaces.MergeConflict{{Type: interfaces.MERGE_TYPE_FILE, ID: metricPath,
				File: metricPath, Reason: interfaces.MERGE_CONFLICT_BOTH_CHANGED}})
			So(result.Files[metricPath], ShouldResemble, ours[metricPath])
		})

		Convey("A set that does not load is an error\n", func() {
			_, err := Merge(base, with(base, map[string]string{networkPath: ""}), base)

			So(err, ShouldNotBeNil)
		})
	})

	Convey("Test Merge of changes that break references across definitions\n", t, func() {
		base := baseSet()
		shipsPath := "relation_types/supplier_ships_part.bkn"
		ships := strings.NewReplacer("supplier_supplies_part", "supplier_ships_part",
			"Supplier supplies part", "Supplier ships part").Replace(relationText)

		Convey("A relation one side adds to an object type the other side deleted is a conflict\n", func() {
			ours := with(base, map[string]string{shipsPath: ships})
			theirs := with(base, map[string]string{supplierPath: "", suppliesPath: ""})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldNotBeEmpty)
			for _, c := range result.Conflicts {
				So(c.Reason, ShouldEqual, interfaces.MERGE_CONFLICT_BROKEN_REFERENCE)
				So(c.Type, ShouldEqual, "relation_type")
				So(c.ID, ShouldEqual, "supplier_ships_part")
				So(c.File, ShouldEqual, shipsPath)
				So(c.Detail, ShouldContainSubstring, "supplier")
			}
			// Each definition is counted once: the new relation moves from Ours to Conflicted.
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 4, Deleted: 2, Conflicted: 1})
		})

		Convey("Two object types added with the same name conflict on network.bkn\n", func() {
			widget := func(id string) string {
				return objectType(id, "Widget", "A widget.", row("id", "string", "Widget id"))
			}
			ours := with(base, map[string]string{"object_types/item.bkn": widget("item")})
			theirs := with(base, map[string]string{"object_types/gadget.bkn": widget("gadget")})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldNotBeEmpty)
			for _, c := range result.Conflicts {
				So(c.Reason, ShouldEqual, interfaces.MERGE_CONFLICT_BROKEN_REFERENCE)
				So(c.Type, ShouldEqual, "network")
				So(c.ID, ShouldEqual, "supply")
				So(c.File, ShouldEqual, networkPath)
				So(c.Detail, ShouldContainSubstring, "Widget")
			}
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 5, Ours: 1, Theirs: 1, Conflicted: 1})
		})

		Convey("Two definitions that claim the same file conflict instead of one overwriting the other\n", func() {
			ours := with(base, map[string]string{partPath: objectType("piece", "Piece", "A part.",
				row("id", "string", "Part number"), row("weight", "float", "Weight in kg"))})
			theirs := with(base, map[string]string{partPath: objectType("component", "Component", "A part.",
				row("id", "string", "Part number"), row("weight", "float", "Weight in kg"))})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			var onFile []interfaces.MergeConflict
			for _, c := range result.Conflicts {
				if c.File == partPath && c.Reason == interfaces.MERGE_CONFLICT_BOTH_CHANGED {
					onFile = append(onFile, c)
				}
			}
			So(onFile, ShouldHaveLength, 1)
			So(onFile[0].ID, ShouldEqual, "component")
			// The file keeps ours, whichever definition sorts first.
			So(findObjectType(loadMerged(result.Files), "piece"), ShouldNotBeNil)
			So(findObjectType(loadMerged(result.Files), "component"), ShouldBeNil)
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 5, Deleted: 1, Ours: 1, Conflicted: 1})
		})

		Convey("A reference already broken on one side is not blamed on the merge\n", func() {
			brokenBase := with(base, map[string]string{supplierPath: ""})

			result, err := Merge(brokenBase, brokenBase, with(brokenBase, map[string]string{partPath: objectType("part", "Part", "Edited.",
				row("id", "string", "Part number"), row("weight", "float", "Weight in kg"))}))

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
		})
	})
}

// v3Set is a BKN 3.0 package in the spelling of the SDK's own 3.0 fixture.
func v3Set() map[string][]byte {
	return map[string][]byte{
		networkPath: []byte("---\ntype: knowledge_network\nid: kn\nname: kn\ntags: []\nbkn_spec: \"3.0\"\n---\n\n# kn\n\nOperations network.\n"),
		"object_types/pod.bkn": []byte("---\ntype: object_type\nid: pod\nname: Pod\ntags: [k8s]\n---\n\n## ObjectType: Pod\n\nA pod.\n\n" +
			"### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | res_pod | pods |\n\n" +
			"### Data Properties\n\n" +
			"| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |\n" +
			"|------|--------------|------|----------|----------|-------------|--------------|\n" +
			"| id | ID | string | | | | id |\n" +
			"| pod_name | Name | string | | true | | pod_name |\n" +
			"| cpu_pct | CPU | float | | | | cpu_pct |\n\n" +
			"### Property Constraints\n\n```yaml\ncpu_pct:\n  range: {min: 0, max: 100}\n  unit: \"%\"\n```\n\n" +
			"### Keys\n\nPrimary Keys: id\nDisplay Key: pod_name\nIncremental Key: \n"),
		"action_types/restart_pod.bkn": []byte("---\ntype: action_type\nid: restart_pod\nname: Restart pod\ntags: []\naction_intent: modify\n---\n\n" +
			"## ActionType: Restart pod\n\nRestarts a failing pod.\n\n" +
			"### Bound Object\n\n| Bound Object |\n|--------------|\n| pod |\n\n" +
			"### Trigger Condition\n\n```yaml\nfield: pod_name\noperation: not_empty\n```\n\n" +
			"### Action Source\n\n| Type | BoxID | ToolID | McpID | ToolName |\n|------|-------|--------|-------|----------|\n| tool | box_k8s | tool_restart | | |\n\n" +
			"### Parameter Binding\n\n| Name | Type | Source | Operation | ValueFrom | Value | Description |\n" +
			"|------|------|--------|-----------|-----------|-------|-------------|\n" +
			"| pod | string | Body | | property | pod_name | Pod name |\n| reason | string | Body | | input | | Reason |\n\n" +
			"### Schedule\n\n| Type | Expression |\n|------|------------|\n\n"),
	}
}

func Test_Merge_V3(t *testing.T) {
	Convey("Test Merge of a BKN 3.0 package\n", t, func() {
		base := v3Set()
		pod := string(base["object_types/pod.bkn"])
		action := string(base["action_types/restart_pod.bkn"])

		Convey("Field merges are written under 3.0 and read back as merged\n", func() {
			ours := with(base, map[string]string{
				"object_types/pod.bkn":         strings.Replace(pod, "| cpu_pct | CPU |", "| cpu_pct | CPU usage |", 1),
				"action_types/restart_pod.bkn": strings.Replace(action, "Restarts a failing pod.", "Restarts a pod that keeps failing.", 1),
			})
			theirs := with(base, map[string]string{
				"object_types/pod.bkn":         strings.Replace(pod, "max: 100", "max: 400", 1),
				"action_types/restart_pod.bkn": strings.Replace(action, "| Reason |", "| Why the pod is restarted |", 1),
			})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldBeEmpty)
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 1, Merged: 2})
			So(result.Files[networkPath], ShouldResemble, base[networkPath])

			net := loadMerged(result.Files)
			So(net.BknSpec, ShouldEqual, "3.0")
			got := findObjectType(net, "pod")
			So(property(got, "cpu_pct").DisplayName, ShouldEqual, "CPU usage")
			So(fmt.Sprint(property(got, "cpu_pct").Constraints.Range.Max), ShouldEqual, "400")
			So(property(got, "pod_name").Required, ShouldBeTrue)
			So(net.ActionTypes, ShouldHaveLength, 1)
			So(net.ActionTypes[0].Description, ShouldEqual, "Restarts a pod that keeps failing.")
			So(net.ActionTypes[0].Parameters[1].Description, ShouldEqual, "Why the pod is restarted")
			So(net.ActionTypes[0].ActionSource.ToolID, ShouldEqual, "tool_restart")
		})

		Convey("A field merge that breaks a rule of the definition itself is a conflict and keeps ours\n", func() {
			ours := with(base, map[string]string{
				"object_types/pod.bkn":         strings.Replace(pod, "min: 0, max: 100", "min: 50, max: 100", 1),
				"action_types/restart_pod.bkn": strings.Replace(action, "Restarts a failing pod.", "Restarts a pod that keeps failing.", 1),
			})
			theirs := with(base, map[string]string{
				"object_types/pod.bkn":         strings.Replace(pod, "min: 0, max: 100", "min: 0, max: 40", 1),
				"action_types/restart_pod.bkn": strings.Replace(action, "| Reason |", "| Why the pod is restarted |", 1),
			})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldHaveLength, 1)
			c := result.Conflicts[0]
			So(c.Reason, ShouldEqual, interfaces.MERGE_CONFLICT_INVALID_DEFINITION)
			So(c.Type, ShouldEqual, "object_type")
			So(c.ID, ShouldEqual, "pod")
			So(c.File, ShouldEqual, "object_types/pod.bkn")
			So(c.Detail, ShouldContainSubstring, "range")
			So(result.Files["object_types/pod.bkn"], ShouldResemble, ours["object_types/pod.bkn"])
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 1, Merged: 1, Conflicted: 1})

			net := loadMerged(result.Files)
			So(net.ActionTypes[0].Parameters[1].Description, ShouldEqual, "Why the pod is restarted")
		})
	})
}

func Test_Merge_V3_Declarations(t *testing.T) {
	Convey("Test Merge of 3.0 declarations that span definitions\n", t, func() {
		const percentPath = "value_types/percent.bkn"
		const nodePath = "object_types/node.bkn"
		percent := "---\ntype: value_type\nid: percent\nname: Percent\ntags: []\n---\n\n## ValueType: Percent\n\nA share of a whole.\n\n" +
			"### Definition\n\n```yaml\nbase_type: float\nunit: \"%\"\n```\n"
		v3 := v3Set()
		pod := string(v3["object_types/pod.bkn"])
		rangeBlock := "cpu_pct:\n  range: {min: 0, max: 100}\n  unit: \"%\"\n"
		podPercent := strings.Replace(pod, rangeBlock, "cpu_pct:\n  value_type: percent\n", 1)
		base := with(v3, map[string]string{percentPath: percent, "object_types/pod.bkn": podPercent})
		// Ours drops the value type and the only reference to it.
		ours := with(base, map[string]string{percentPath: "", "object_types/pod.bkn": pod})
		node := strings.NewReplacer("id: pod\n", "id: node\n", "Pod", "Node", "A pod.", "A node.", "res_pod", "res_node", "pods", "nodes").Replace(podPercent)

		Convey("A definition one side adds with a value type the other side deleted is a conflict\n", func() {
			theirs := with(base, map[string]string{nodePath: node})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldHaveLength, 1)
			c := result.Conflicts[0]
			So(c.Reason, ShouldEqual, interfaces.MERGE_CONFLICT_BROKEN_REFERENCE)
			So(c.Type, ShouldEqual, "object_type")
			So(c.ID, ShouldEqual, "node")
			So(c.File, ShouldEqual, nodePath)
			So(c.Detail, ShouldContainSubstring, "percent")
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 2, Ours: 1, Deleted: 1, Conflicted: 1})
		})

		Convey("A field merge that references a deleted value type is a conflict, not an error\n", func() {
			theirs := with(base, map[string]string{"object_types/pod.bkn": strings.Replace(
				strings.Replace(podPercent, "| cpu_pct | CPU | float | | | | cpu_pct |", "| cpu_pct | CPU | float | | | | cpu_pct |\n| mem_pct | Memory | float | | | | mem_pct |", 1),
				"cpu_pct:\n  value_type: percent\n", "cpu_pct:\n  value_type: percent\nmem_pct:\n  value_type: percent\n", 1)})

			result, err := Merge(base, ours, theirs)

			So(err, ShouldBeNil)
			So(result.Conflicts, ShouldHaveLength, 1)
			c := result.Conflicts[0]
			So(c.Reason, ShouldEqual, interfaces.MERGE_CONFLICT_BROKEN_REFERENCE)
			So(c.Type, ShouldEqual, "object_type")
			So(c.ID, ShouldEqual, "pod")
			So(c.File, ShouldEqual, "object_types/pod.bkn")
			So(c.Detail, ShouldContainSubstring, "mem_pct")
			So(string(result.Files["object_types/pod.bkn"]), ShouldContainSubstring, "mem_pct:\n  value_type: percent")
			So(result.Summary, ShouldResemble, interfaces.MergeSummary{Unchanged: 2, Deleted: 1, Conflicted: 1})
		})
	})
}
