// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const (
	token   = "Bearer test-token"
	oldNet  = "supply_kn"
	newNet  = "supply_kn_v1_1_0"
	boxID   = "26299643-33b3-4148-a7fc-b14ec705d834"
	toolID  = "ab6cb7d0-9d12-4b08-86df-ab5b35779b49"
	skillID = "skill-1"
)

// toolboxExport is an impex export of one function toolbox whose code reads network code.
func toolboxExport(code string) []byte {
	return []byte(fmt.Sprintf(`{"toolbox":{"configs":[{"box_id":%q,"box_name":"supply_tools","category_name":"Other",
"box_svc_url":"http://box","metadata_type":"function","update_time":1761400000000000001,
"tools":[{"tool_id":%q,"name":"expand_bom","metadata_type":"function","code":%q}]}]},
"operator":{"configs":[{"operator_id":"op1"}]}}`, boxID, toolID, code))
}

func skillFiles(body string) map[string][]byte {
	return map[string][]byte{
		SKILL_MD:       []byte("---\nname: plan-supply\ndescription: Plans supply.\n---\n" + body),
		"refs/kn.md":   []byte("Metrics of the bound network.\n"),
		"assets/a.bin": {0xff, 0xfe, 's', 'u', 'p', 'p', 'l', 'y'},
	}
}

func mustFunction(code string) *Item {
	it, err := NewFunction(toolboxExport(code))
	So(err, ShouldBeNil)
	return it
}

func mustSkill(body string) *Item {
	it, err := NewSkill(skillID, skillFiles(body))
	So(err, ShouldBeNil)
	return it
}

func Test_NewFunction(t *testing.T) {
	Convey("Test NewFunction\n", t, func() {
		Convey("The export is stored without category_name, numbers kept as written\n", func() {
			it := mustFunction("print(1)")
			So(it.Kind, ShouldEqual, KIND_FUNCTION)
			So(it.ID, ShouldEqual, boxID)
			So(it.Name, ShouldEqual, "supply_tools")
			So(it.Dir(), ShouldEqual, "functions/"+boxID)
			stored := string(it.Files[TOOLBOX_FILE])
			So(stored, ShouldNotContainSubstring, "category_name")
			So(stored, ShouldContainSubstring, "1761400000000000001")
			So(it.Digest, ShouldStartWith, "sha256:")
		})

		Convey("The category name of another language does not change the digest\n", func() {
			other := strings.Replace(string(toolboxExport("print(1)")), `"Other"`, `"其他"`, 1)
			it, err := NewFunction([]byte(other))
			So(err, ShouldBeNil)
			So(it.Digest, ShouldEqual, mustFunction("print(1)").Digest)
		})

		Convey("An export without exactly one toolbox is refused\n", func() {
			_, err := NewFunction([]byte(`{"toolbox":{"configs":[]}}`))
			So(err, ShouldNotBeNil)
			_, err = NewFunction([]byte(`not json`))
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_Plan(t *testing.T) {
	Convey("Test Plan\n", t, func() {
		fn, skill := mustFunction("kn_id = params['kn_id']"), mustSkill("Read the bound network.\n")
		previous := &Manifest{
			Functions: []*ManifestEntry{{ID: boxID, Digest: fn.Digest, Networks: []string{oldNet}}},
			Skills:    []*ManifestEntry{{ID: skillID, Digest: skill.Digest, Networks: []string{oldNet}}},
		}

		Convey("Unchanged items that do not name the network are reused\n", func() {
			steps := Plan(previous, []*Item{fn, skill}, oldNet)
			So(steps, ShouldHaveLength, 2)
			So(steps[0].Copy, ShouldBeFalse)
			So(steps[1].Copy, ShouldBeFalse)
		})

		Convey("An item the previous release did not record is reused\n", func() {
			steps := Plan(&Manifest{}, []*Item{fn}, oldNet)
			So(steps[0].Copy, ShouldBeFalse)
		})

		Convey("A changed item is copied\n", func() {
			changed := mustFunction("kn_id = params['kn_id']  # tuned")
			steps := Plan(previous, []*Item{changed, skill}, oldNet)
			So(steps[0].Copy, ShouldBeTrue)
			So(steps[0].Reasons, ShouldResemble, []string{REASON_CHANGED})
			So(steps[1].Copy, ShouldBeFalse)
		})

		Convey("An item naming the old network id is copied, a longer word is not that id\n", func() {
			hardcoded := mustFunction(`KN_ID = "supply_kn"`)
			previous.Functions[0].Digest = hardcoded.Digest
			wordy := mustSkill("Covers supply_kn_chain and supply_kn-side planning.\n")
			previous.Skills[0].Digest = wordy.Digest

			steps := Plan(previous, []*Item{hardcoded, wordy}, oldNet)
			So(steps[0].Copy, ShouldBeTrue)
			So(steps[0].Reasons, ShouldResemble, []string{REASON_NETWORK_ID})
			So(steps[1].Copy, ShouldBeFalse)
		})
	})
}

func Test_NextManifest(t *testing.T) {
	Convey("Test NextManifest\n", t, func() {
		fn, skill := mustFunction("x"), mustSkill("y")
		previous := &Manifest{Functions: []*ManifestEntry{{ID: boxID, Digest: fn.Digest, Networks: []string{oldNet}}}}

		next := NextManifest(previous, []*Item{skill, fn}, newNet)

		So(next.Functions, ShouldResemble, []*ManifestEntry{{ID: boxID, Name: "supply_tools", Digest: fn.Digest,
			Networks: []string{oldNet, newNet}}})
		So(next.Skills, ShouldResemble, []*ManifestEntry{{ID: skillID, Name: "plan-supply", Digest: skill.Digest,
			Networks: []string{newNet}}})
		data, err := next.Marshal()
		So(err, ShouldBeNil)
		again, err := ParseManifest(data)
		So(err, ShouldBeNil)
		So(again, ShouldResemble, next)
		empty, err := ParseManifest(nil)
		So(err, ShouldBeNil)
		So(empty.Empty(), ShouldBeTrue)
	})
}

func Test_CopyFunction(t *testing.T) {
	Convey("Test CopyFunction\n", t, func() {
		Convey("The copy has new ids and name, the new network id and no operators\n", func() {
			it := mustFunction(`KN_ID = "supply_kn"; act = KN_ID + "_act"; other = "supply_kn_chain"`)

			payload, c, err := CopyFunction(it, oldNet, newNet)

			So(err, ShouldBeNil)
			So(c.OldID, ShouldEqual, boxID)
			So(c.NewID, ShouldNotEqual, boxID)
			So(c.NewName, ShouldEqual, "supply_tools_supply_kn_v1_1_0")
			So(c.ToolIDs[toolID], ShouldNotBeEmpty)
			So(c.ToolIDs[toolID], ShouldNotEqual, toolID)
			doc := map[string]any{}
			So(json.Unmarshal(payload, &doc), ShouldBeNil)
			So(doc["operator"], ShouldBeNil)
			box := doc["toolbox"].(map[string]any)["configs"].([]any)[0].(map[string]any)
			So(box["box_id"], ShouldEqual, c.NewID)
			So(box["box_name"], ShouldEqual, c.NewName)
			tool := box["tools"].([]any)[0].(map[string]any)
			So(tool["tool_id"], ShouldEqual, c.ToolIDs[toolID])
			So(tool["code"], ShouldEqual, `KN_ID = "supply_kn_v1_1_0"; act = KN_ID + "_act"; other = "supply_kn_chain"`)

			// A retry derives the same ids.
			_, again, _ := CopyFunction(it, oldNet, newNet)
			So(again, ShouldResemble, c)
		})

		Convey("The name of an earlier copy loses its suffix and fits 50 characters\n", func() {
			So(copyName("tools_supply", "supply", "supply_v1_1_0", 50), ShouldEqual, "tools_supply_v1_1_0")
			So(copyName("tools_supply_x", "supply-x", "supply-x_v1_1_0", 50), ShouldEqual, "tools_supply_x_v1_1_0")
			long := strings.Repeat("长", 60)
			name := copyName(long, oldNet, "a_very_long_lineage_identifier_v10_20_30", 50)
			So([]rune(name), ShouldHaveLength, 50)
			So(name, ShouldEndWith, "_a_very_long_lineage_identifier_v10_20_30")
		})
	})
}

func Test_CopySkill(t *testing.T) {
	Convey("Test CopySkill\n", t, func() {
		Convey("Text files get the new network id and SKILL.md a new name; binary files stay\n", func() {
			it := mustSkill("Query network supply_kn for capacity.\n")

			files, c, err := CopySkill(it, oldNet, newNet)

			So(err, ShouldBeNil)
			So(c.Kind, ShouldEqual, KIND_SKILL)
			So(c.NewName, ShouldEqual, "plan-supply_supply_kn_v1_1_0")
			So(c.NewID, ShouldBeEmpty)
			So(string(files[SKILL_MD]), ShouldEqual,
				"---\nname: plan-supply_supply_kn_v1_1_0\ndescription: Plans supply.\n---\nQuery network supply_kn_v1_1_0 for capacity.\n")
			So(files["assets/a.bin"], ShouldResemble, it.Files["assets/a.bin"])

			archive, err := SkillArchive(files)
			So(err, ShouldBeNil)
			zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
			So(err, ShouldBeNil)
			So(zr.File, ShouldHaveLength, 3)
			r, _ := zr.Open(SKILL_MD)
			md, _ := io.ReadAll(r)
			So(string(md), ShouldEqual, string(files[SKILL_MD]))
		})

		Convey("A SKILL.md whose name is a block value is refused\n", func() {
			it := &Item{Kind: KIND_SKILL, ID: skillID, Files: map[string][]byte{SKILL_MD: []byte("---\nname: |\n  x\n---\n")}}
			_, _, err := CopySkill(it, oldNet, newNet)
			So(err, ShouldNotBeNil)
		})
	})
}

const networkWithCapabilities = `---
type: knowledge_network
id: supply_kn
name: Supply chain
capabilities:
  skills:
    - id: skill-1
      name: plan-supply
    - name: by-name-only
  functions:
    - box_id: 26299643-33b3-4148-a7fc-b14ec705d834
      tool_id: ab6cb7d0-9d12-4b08-86df-ab5b35779b49
      box_name: supply_tools
      tool_name: expand_bom
    - box_id: other-box
      tool_id: other-tool
---

# Supply chain
`

const actionType = `---
type: action_type
id: replenish
name: Replenish
action_type: add
---

## ActionType: Replenish

### Bound Object

| Bound Object |
|--------------|
| part |

### Action Source

| Type | BoxID | ToolID | McpID | ToolName |
|------|-------|--------|-------|----------|
| tool | 26299643-33b3-4148-a7fc-b14ec705d834 | ab6cb7d0-9d12-4b08-86df-ab5b35779b49 |  |  |

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| kn_id | string | Body |  | const | supply_kn | network |
| part_id | string | Body |  | property | supply_kn | not a const |
| note | string | Body |  | const | supply_kn_chain | another word |
`

func Test_RewritePackage(t *testing.T) {
	Convey("Test RewritePackage\n", t, func() {
		pkg := map[string][]byte{
			"network.bkn":                []byte(networkWithCapabilities),
			"action_types/replenish.bkn": []byte(actionType),
			"README.md":                  []byte("| " + boxID + " |\n"),
		}
		fnCopy := &Copy{Kind: KIND_FUNCTION, OldID: boxID, NewID: "box-new", NewName: "supply_tools_supply_kn_v1_1_0",
			ToolIDs: map[string]string{toolID: "tool-new"}}
		skillCopy := &Copy{Kind: KIND_SKILL, OldID: skillID, NewID: "skill-new", NewName: "plan-supply_supply_kn_v1_1_0"}

		Convey("Action sources, const network ids and capabilities move to the copies\n", func() {
			out, err := RewritePackage(pkg, oldNet, newNet, []*Copy{fnCopy, skillCopy})

			So(err, ShouldBeNil)
			at := string(out["action_types/replenish.bkn"])
			So(at, ShouldContainSubstring, "| tool | box-new | tool-new |  |  |")
			So(at, ShouldContainSubstring, "| kn_id | string | Body |  | const | supply_kn_v1_1_0 | network |")
			So(at, ShouldContainSubstring, "| part_id | string | Body |  | property | supply_kn | not a const |")
			So(at, ShouldContainSubstring, "| note | string | Body |  | const | supply_kn_chain | another word |")
			// Only .bkn files are definitions.
			So(out["README.md"], ShouldResemble, pkg["README.md"])

			network, err := bkn.ParseNetworkFile(string(out["network.bkn"]), "network.bkn")
			So(err, ShouldBeNil)
			So(network.Capabilities.Skills[0], ShouldResemble, &bkn.BknCapabilitySkill{ID: "skill-new", Name: "plan-supply_supply_kn_v1_1_0"})
			So(network.Capabilities.Skills[1], ShouldResemble, &bkn.BknCapabilitySkill{Name: "by-name-only"})
			So(network.Capabilities.Functions[0], ShouldResemble, &bkn.BknCapabilityFunction{
				BoxID: "box-new", ToolID: "tool-new", BoxName: "supply_tools_supply_kn_v1_1_0", ToolName: "expand_bom"})
			So(network.Capabilities.Functions[1], ShouldResemble, &bkn.BknCapabilityFunction{BoxID: "other-box", ToolID: "other-tool"})
			// Lines outside the rewritten values are kept.
			So(string(out["network.bkn"]), ShouldStartWith, "---\ntype: knowledge_network\nid: supply_kn\nname: Supply chain\n")
		})

		Convey("Without copies only the const network id moves and network.bkn is untouched\n", func() {
			out, err := RewritePackage(pkg, oldNet, newNet, nil)

			So(err, ShouldBeNil)
			So(out["network.bkn"], ShouldResemble, pkg["network.bkn"])
			at := string(out["action_types/replenish.bkn"])
			So(at, ShouldContainSubstring, "| tool | "+boxID+" | "+toolID+" |  |  |")
			So(at, ShouldContainSubstring, "| const | supply_kn_v1_1_0 |")
		})

		Convey("Capabilities in flow style are refused when they must move\n", func() {
			pkg["network.bkn"] = []byte("---\ntype: knowledge_network\nid: supply\nname: S\ncapabilities:\n  skills:\n    - {id: skill-1, name: plan-supply}\n---\n")
			_, err := RewritePackage(pkg, oldNet, newNet, []*Copy{skillCopy})
			So(err, ShouldNotBeNil)
		})

		Convey("References finds the toolboxes and Skills the network uses\n", func() {
			fsys := bkn.NewMemoryFileSystem()
			fsys.AddFiles(pkg)
			network, err := bkn.LoadNetworkWithFS(fsys, ".")
			So(err, ShouldBeNil)
			boxes, skills := References(network)
			So(boxes, ShouldResemble, []string{boxID, "other-box"})
			So(skills, ShouldResemble, []string{skillID})
		})
	})
}

func Test_Apply(t *testing.T) {
	Convey("Test Apply\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		efa := pmock.NewMockExecutionFactoryAccess(mockCtrl)
		ctx := context.Background()

		Convey("Reused items stay, copies are imported, registered and read back\n", func() {
			fn := mustFunction(`KN_ID = "supply_kn"`)
			skill := mustSkill("Network supply_kn.\n")
			reused := mustFunction("kn_id = params['kn_id']")
			reused.ID = "reused-box"
			steps := []*Step{{Item: reused}, {Item: fn, Copy: true}, {Item: skill, Copy: true}}
			var imported []byte
			var newBoxID string

			efa.EXPECT().ImportToolbox(gomock.Any(), token, gomock.Any(), interfaces.IMPEX_MODE_UPSERT).DoAndReturn(
				func(_ context.Context, _ string, data []byte, _ string) error {
					imported = data
					return nil
				})
			efa.EXPECT().ExportToolbox(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, id string) ([]byte, error) {
					newBoxID = id
					return imported, nil
				})
			efa.EXPECT().FindPublishedSkill(gomock.Any(), token, "plan-supply_supply_kn_v1_1_0").Return("", nil)
			efa.EXPECT().RegisterSkill(gomock.Any(), token, gomock.Any()).Return("skill-new", nil)
			efa.EXPECT().PublishSkill(gomock.Any(), token, "skill-new").Return(nil)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, "skill-new").Return(skillFiles("Network supply_kn_v1_1_0.\n"), nil)

			copies, items, err := Apply(ctx, efa, token, steps, oldNet, newNet)

			So(err, ShouldBeNil)
			So(copies, ShouldHaveLength, 2)
			So(copies[0].NewID, ShouldEqual, newBoxID)
			So(copies[1].NewID, ShouldEqual, "skill-new")
			So(items, ShouldHaveLength, 3)
			So(items[0], ShouldEqual, reused)
			So(items[1].ID, ShouldEqual, newBoxID)
			So(string(items[1].Files[TOOLBOX_FILE]), ShouldContainSubstring, `KN_ID = \"supply_kn_v1_1_0\"`)
			So(items[2].ID, ShouldEqual, "skill-new")
		})

		Convey("A Skill copy a failed attempt already published is taken, not registered again\n", func() {
			skill := mustSkill("Network supply_kn.\n")
			efa.EXPECT().FindPublishedSkill(gomock.Any(), token, "plan-supply_supply_kn_v1_1_0").Return("skill-old-copy", nil)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, "skill-old-copy").Return(skillFiles("x"), nil)

			copies, _, err := Apply(ctx, efa, token, []*Step{{Item: skill, Copy: true}}, oldNet, newNet)

			So(err, ShouldBeNil)
			So(copies[0].NewID, ShouldEqual, "skill-old-copy")
		})

		Convey("A refused copy names the item\n", func() {
			fn := mustFunction(`KN_ID = "supply_kn"`)
			refused := &interfaces.ExecutionFactoryError{HTTPCode: http.StatusForbidden}
			efa.EXPECT().ImportToolbox(gomock.Any(), token, gomock.Any(), gomock.Any()).Return(refused)

			_, _, err := Apply(ctx, efa, token, []*Step{{Item: fn, Copy: true}}, oldNet, newNet)

			var itemErr *ItemError
			So(errors.As(err, &itemErr), ShouldBeTrue)
			So(itemErr.Kind, ShouldEqual, KIND_FUNCTION)
			So(itemErr.ID, ShouldEqual, boxID)
			So(itemErr.Name, ShouldEqual, "supply_tools")
			So(itemErr.Operation, ShouldEqual, OPERATION_COPY)
			So(errors.Is(err, refused), ShouldBeTrue)
		})

		Convey("An export of the wrong toolbox is refused\n", func() {
			efa.EXPECT().ExportToolbox(gomock.Any(), token, "box-x").Return(toolboxExport("x"), nil)
			_, err := ExportFunction(ctx, efa, token, "box-x")
			var itemErr *ItemError
			So(errors.As(err, &itemErr), ShouldBeTrue)
			So(itemErr.Operation, ShouldEqual, OPERATION_EXPORT)
		})
	})
}
