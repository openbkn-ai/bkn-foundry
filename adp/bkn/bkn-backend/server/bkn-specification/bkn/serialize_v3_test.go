// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportFiles writes a network to tar and returns its files by path.
func exportFiles(t *testing.T, doc *BknNetwork) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, WriteNetworkToTar(doc, &buf))
	files := map[string]string{}
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[hdr.Name] = string(data)
	}
	return files
}

// A 3.0 export reads back under 3.0 and exports again byte for byte.
func TestWriteV3_RoundTripIsByteIdentical(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	first := exportFiles(t, net)

	again, err := loadFiles(first)
	require.NoError(t, err, "the exporter's own output is valid 3.0")
	assert.Equal(t, first, exportFiles(t, again))
}

func TestWriteV3_WritesTheOneSpelling(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	files := exportFiles(t, net)

	assert.Contains(t, files["network.bkn"], "bkn_spec: \"3.0\"\n")
	obj := files["object_types/pod.bkn"]
	assert.Contains(t, obj, "| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |\n")
	assert.Contains(t, obj, "| id | ID | string |  |  |  | id |\n", "a primary key's Required stays empty")
	assert.Contains(t, obj, "| pod_name | 名称 | string |  | true |  | pod_name |\n")
	assert.Contains(t, obj, "### Property Constraints\n\n```yaml\ncreated_at:\n  timezone: Asia/Shanghai\ncpu_pct:\n  range: {min: 0, max: 100}\n  unit: '%'\n```\n",
		"properties in table order, keys in the order of §6.8")
	assert.Less(t, strings.Index(obj, "### Property Constraints"), strings.Index(obj, "### Logic Properties"))

	act := files["action_types/restart_pod.bkn"]
	assert.Contains(t, act, "action_intent: modify\n")
	assert.NotContains(t, act, "action_type:")
	assert.NotContains(t, act, "### Affect Object", "3.0 writes Affect Object only when it has content")
}

func TestWriteV3_KeepsFrontmatterStrings(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	ot := net.ObjectTypes[0]
	ot.ID, ot.Name, ot.Tags = "001", "订单: 明细", []string{"1.0", "true"}
	net.Version = "1.0"
	net.ConceptGroups[0].ObjectTypes = []string{"001"}
	net.ActionTypes[0].BoundObject = "001"
	net.ActionTypes[0].ImpactContracts[0].ObjectTypeID = "001"
	net.RelationTypes[0].Endpoint.Source, net.RelationTypes[0].Endpoint.Target = "001", "001"

	files := exportFiles(t, net)
	again, err := loadFiles(files)
	require.NoError(t, err)
	got := again.ObjectTypes[0]
	assert.Equal(t, "001", got.ID)
	assert.Equal(t, "订单: 明细", got.Name)
	assert.Equal(t, []string{"1.0", "true"}, got.Tags)
	assert.Equal(t, "1.0", again.Version)
	assert.Equal(t, files, exportFiles(t, again))
}

func TestWriteV3_EscapesDescriptionHeadings(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	desc := "说明。\n### 标题\n\\# 一个反斜杠\n\\\\# 两个反斜杠\n# 一级"
	net.ObjectTypes[0].Description, net.ObjectTypes[0].Summary = desc, ExtractSummary(desc)
	net.Description = desc

	files := exportFiles(t, net)
	assert.Contains(t, files["object_types/pod.bkn"], "说明。\n\\### 标题\n\\\\# 一个反斜杠\n\\\\\\# 两个反斜杠\n\\# 一级\n")
	again, err := loadFiles(files)
	require.NoError(t, err)
	assert.Equal(t, desc, again.ObjectTypes[0].Description)
	assert.Equal(t, desc, again.Description)
	assert.Equal(t, files, exportFiles(t, again))
}

// A network built without bkn_spec (the backend's export) but carrying a declaration is written
// under 3.0, so the declaration survives the next import.
func TestWriteNetwork_DeclarationsSelectV3(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	net.BknSpec = BknSpecLegacy
	require.True(t, HasV3Declarations(net))
	assert.Contains(t, exportFiles(t, net)["network.bkn"], "bkn_spec: \"3.0\"")
}

// A network without any 3.0 declaration is written exactly as before.
func TestWriteNetwork_LegacyStaysLegacy(t *testing.T) {
	net, err := loadV3(t, v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""})
	require.NoError(t, err)
	require.False(t, HasV3Declarations(net))
	files := exportFiles(t, net)
	assert.NotContains(t, files["network.bkn"], "bkn_spec")
	assert.Equal(t, SerializeObjectType(net.ObjectTypes[0]), files["object_types/pod.bkn"])
	assert.Equal(t, SerializeActionType(net.ActionTypes[0]), files["action_types/restart_pod.bkn"])
	assert.Equal(t, SerializeBknNetwork(net), files["network.bkn"])

	// Required on a primary key alone is implied, not a declaration.
	for _, p := range net.ObjectTypes[0].DataProperties {
		p.Required = p.Name == "id"
	}
	assert.False(t, HasV3Declarations(net))
}
