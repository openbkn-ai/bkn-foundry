// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lateOrderFile = "object_types/late_pod.bkn"

// lateOrder is an event class as a package carries it: its kind and its configuration, nothing
// the platform keeps. It is written the same way under 2.1 and 3.0.
var lateOrder = "---\ntype: object_type\nid: late_pod\nname: Pod 重启过多\ntags: []\nkinds: [event]\n---\n\n" +
	"## ObjectType: Pod 重启过多\n\n每个对象是一条“Pod 重启过多”事件。\n\n" +
	"### Event\n\n```yaml\nevent:\n  source: rule\n  subject: pod\n  severity: high\n  merge_hours: 1.5\n  handling_note: 查看最近的发布。\n```\n"

// v21Files is a 2.1 package: the 2.x rules, and an event class.
var v21Files = map[string]string{
	"network.bkn": "---\ntype: knowledge_network\nid: kn\nname: kn\ntags: []\nbkn_spec: \"2.1\"\n---\n\n# kn\n\n运维网络。\n",
	"object_types/pod.bkn": "---\ntype: object_type\nid: pod\nname: Pod\ntags: [k8s]\n---\n\n## ObjectType: Pod\n\nPod 描述。\n\n" +
		"### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | res_pod | pods |\n\n" +
		"### Data Properties\n\n| Name | Display Name | Type | Description | Mapped Field |\n" +
		"|------|--------------|------|-------------|--------------|\n" +
		"| id | ID | string | | id |\n| pod_name | 名称 | string | | pod_name |\n\n" +
		"### Logic Properties\n\n### Keys\n\nPrimary Keys: id\nDisplay Key: pod_name\nIncremental Key: \n",
	lateOrderFile: lateOrder,
}

func objectTypeOf(net *BknNetwork, id string) *BknObjectType {
	for _, ot := range net.ObjectTypes {
		if ot.ID == id {
			return ot
		}
	}
	return nil
}

// eventSpecs load a package with the event class, changed by replacing old with new, under each
// specification that has event classes.
var eventSpecs = map[string]func(t *testing.T, old, new string, extra ...v3Edit) (*BknNetwork, error){
	BknSpecV21: func(t *testing.T, old, new string, extra ...v3Edit) (*BknNetwork, error) {
		files := map[string]string{}
		for path, text := range v21Files {
			files[path] = text
		}
		files[lateOrderFile] = changed(t, lateOrder, old, new)
		for _, e := range extra {
			files[e.file] = e.new
		}
		return loadFiles(files)
	},
	BknSpecV3: func(t *testing.T, old, new string, extra ...v3Edit) (*BknNetwork, error) {
		return loadV3(t, append([]v3Edit{{file: lateOrderFile, new: changed(t, lateOrder, old, new)}}, extra...)...)
	},
}

func changed(t *testing.T, text, old, new string) string {
	t.Helper()
	if old == "" {
		return text
	}
	require.Equal(t, 1, strings.Count(text, old), "the event class must contain %q once", old)
	return strings.Replace(text, old, new, 1)
}

func TestEventClassIsReadFromItsKindAndItsSection(t *testing.T) {
	for spec, load := range eventSpecs {
		t.Run(spec, func(t *testing.T) {
			net, err := load(t, "", "")
			require.NoError(t, err)
			assert.Equal(t, spec, net.BknSpec)
			class := objectTypeOf(net, "late_pod")
			require.NotNil(t, class)
			assert.Equal(t, []string{ObjectTypeKindEvent}, class.Kinds)
			assert.True(t, class.IsEventClass())
			assert.Equal(t, &ObjectTypeEvent{Source: "rule", Subject: "pod", Severity: "high", MergeHours: 1.5, HandlingNote: "查看最近的发布。"}, class.Event)
			assert.Equal(t, "每个对象是一条“Pod 重启过多”事件。", class.Description, "### Event is no part of the description")
			assert.Nil(t, class.DataSource)
			assert.Empty(t, class.DataProperties)
			assert.False(t, objectTypeOf(net, "pod").IsEventClass())
			assert.True(t, HasEventClasses(net))

			result := ValidateNetwork(net)
			assert.Empty(t, result.Errors, "an event class needs none of the sections the platform keeps")
		})
	}
}

func TestEventClassIsRefusedWhereItIsWrittenWrong(t *testing.T) {
	config := "event:\n  source: rule\n  subject: pod\n  severity: high\n  merge_hours: 1.5\n  handling_note: 查看最近的发布。\n"
	for name, edit := range map[string][2]string{
		"kinds as a string":      {"kinds: [event]", "kinds: event"},
		"an unknown kind":        {"kinds: [event]", "kinds: [event, timeseries]"},
		"a kind twice":           {"kinds: [event]", "kinds: [event, event]"},
		"no section":             {"### Event\n\n```yaml\n" + config + "```\n", ""},
		"a section without kind": {"kinds: [event]\n", ""},
		"an unknown key":         {"  severity: high\n", "  severity: high\n  owner: ops\n"},
		"no event key":           {config, "source: rule\n"},
		"a section as a table":   {"```yaml\n" + config + "```", "| Source |\n|--------|\n| rule |"},
		"an unknown source":      {"source: rule", "source: sensor"},
		"no subject":             {"  subject: pod\n", ""},
		"itself as subject":      {"subject: pod", "subject: late_pod"},
		"a long severity":        {"severity: high", "severity: " + strings.Repeat("s", EventSeverityMax+1)},
		"a negative window":      {"merge_hours: 1.5", "merge_hours: -1"},
		"a long handling note":   {"handling_note: 查看最近的发布。", "handling_note: " + strings.Repeat("处", EventHandlingNoteMax+1)},
		"a long id":              {"id: late_pod", "id: " + strings.Repeat("e", EventClassIDMax+1)},
	} {
		for spec, load := range eventSpecs {
			t.Run(spec+"/"+name, func(t *testing.T) {
				_, err := load(t, edit[0], edit[1])
				require.Error(t, err)
			})
		}
	}
}

// kinds and ### Event come with 2.1: a package that declares no version, or 2.0, reads them as it
// reads what it does not know.
func TestEventClassIsNotReadUnder20(t *testing.T) {
	class, err := ParseObjectTypeFile(lateOrder, lateOrderFile)
	require.NoError(t, err)
	assert.Empty(t, class.Kinds)
	assert.Nil(t, class.Event)
	assert.Contains(t, class.Description, "### Event")

	files := map[string]string{}
	for path, text := range v21Files {
		files[path] = text
	}
	files["network.bkn"] = strings.Replace(files["network.bkn"], "bkn_spec: \"2.1\"\n", "", 1)
	net, err := loadFiles(files)
	require.NoError(t, err)
	assert.False(t, HasEventClasses(net))
}

func TestSpecVersion21(t *testing.T) {
	for raw, want := range map[any]string{"2.1": BknSpecV21, "2.1.3": BknSpecV21, 2.1: BknSpecV21} {
		spec, err := normalizeBknSpec(raw)
		require.NoError(t, err, "%v", raw)
		assert.Equal(t, want, spec, "%v", raw)
	}
	_, err := normalizeBknSpec("2.2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2.0, 2.1 and 3.0")
}

func TestEventClassInNetwork(t *testing.T) {
	// The relation the platform keeps from the class to its subject is no part of a package.
	relation := "---\ntype: relation_type\nid: late_pod_subject\nname: 关于\ntags: []\n---\n\n## RelationType: 关于\n\n" +
		"### Endpoint\n\n| Source | Target | Type |\n|--------|--------|------|\n| late_pod | pod | direct |\n\n" +
		"### Mapping Rules\n\n| Source Property | Target Property |\n|-----------------|-----------------|\n| object_key | id |\n"
	for spec, load := range eventSpecs {
		t.Run(spec, func(t *testing.T) {
			net, err := load(t, "subject: pod", "subject: order")
			require.NoError(t, err)
			result := ValidateNetwork(net)
			require.Len(t, result.Errors, 1)
			assert.Equal(t, "invalid_event_subject_ref", result.Errors[0].Code)

			net, err = load(t, "", "", v3Edit{file: "relation_types/late_pod_subject.bkn", new: relation})
			require.NoError(t, err)
			codes := []string{}
			for _, issue := range ValidateNetwork(net).Errors {
				codes = append(codes, issue.Code)
			}
			assert.Contains(t, codes, "platform_relation")
		})
	}
}

// An event class is exported as its kind and its configuration, and reads back as it was. A 2.1
// package declares 2.1 and writes everything else as 2.0 does.
func TestEventClassExportsWithoutWhatThePlatformKeeps(t *testing.T) {
	for spec, load := range eventSpecs {
		t.Run(spec, func(t *testing.T) {
			net, err := load(t, "", "")
			require.NoError(t, err)
			class := objectTypeOf(net, "late_pod")
			// What the platform filled in before the export.
			class.DataSource = &ResourceInfo{Type: DATA_SOURCE_TYPE_RESOURCE, ID: "kn_event_x"}
			class.DataProperties = []*DataProperty{{Name: "event_id", Type: "string", MappedField: "event_id"}}
			class.PrimaryKeys, class.DisplayKey = []string{"event_id"}, "event_id"

			files := exportFiles(t, net)
			written := files[lateOrderFile]
			assert.Contains(t, written, "kinds: [event]")
			assert.Contains(t, written, "### Event\n\n```yaml\nevent:\n  source: rule\n  subject: pod\n  severity: high\n  merge_hours: 1.5\n")
			for _, section := range []string{"### Data Source", "### Data Properties", "### Logic Properties", "### Keys"} {
				assert.NotContains(t, written, section)
			}
			assert.Contains(t, files["network.bkn"], "bkn_spec: \""+spec+"\"")
			again, err := loadFiles(files)
			require.NoError(t, err, "the exporter's own output reads back")
			assert.Equal(t, spec, again.BknSpec)
			assert.Equal(t, objectTypeOf(net, "late_pod").Event, objectTypeOf(again, "late_pod").Event)
			assert.Equal(t, files, exportFiles(t, again))
		})
	}
}

// 2.1 adds the event classes and nothing else: a network with them and no 3.0 declaration has
// nothing that needs 3.0.
func TestEventClassesNeedNo30(t *testing.T) {
	net, err := eventSpecs[BknSpecV21](t, "", "")
	require.NoError(t, err)
	assert.False(t, HasV3Declarations(net))
	files := exportFiles(t, net)
	assert.Contains(t, files["object_types/pod.bkn"], "### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | res_pod | pods |")
	assert.NotContains(t, files["object_types/pod.bkn"], "kinds")
}

func TestMergeWindowIsWrittenAsPeopleWriteIt(t *testing.T) {
	net, err := eventSpecs[BknSpecV21](t, "merge_hours: 1.5", "merge_hours: 24")
	require.NoError(t, err)
	assert.Contains(t, exportFiles(t, net)[lateOrderFile], "  merge_hours: 24\n")
}
