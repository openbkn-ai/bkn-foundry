// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/capability"
)

const (
	capBoxID   = "26299643-33b3-4148-a7fc-b14ec705d834"
	capToolID  = "ab6cb7d0-9d12-4b08-86df-ab5b35779b49"
	capSkillID = "skill-1"
)

// capabilityDefinitions are definitions whose action calls a toolbox, with
// the network id bound as a const parameter, and whose network binds a Skill.
func capabilityDefinitions(networkID string, partDesc string) map[string][]byte {
	files := definitions(partDesc, "A supplier.")
	files["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain\n---",
		"name: Supply chain\ncapabilities:\n  skills:\n    - id: "+capSkillID+"\n      name: plan-supply\n---", 1))
	files["action_types/replenish.bkn"] = []byte(fmt.Sprintf(`---
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
| tool | %s | %s |  |  |

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| kn_id | string | Body |  | const | %s | network |
`, capBoxID, capToolID, networkID))
	return files
}

func capabilityExport(code string) []byte {
	return []byte(fmt.Sprintf(`{"toolbox":{"configs":[{"box_id":%q,"box_name":"supply_tools","box_svc_url":"http://box",
"metadata_type":"function","tools":[{"tool_id":%q,"name":"expand_bom","code":%q}]}]}}`, capBoxID, capToolID, code))
}

func releasedSkill(body string) map[string][]byte {
	return map[string][]byte{"SKILL.md": []byte("---\nname: plan-supply\ndescription: Plans.\n---\n" + body)}
}

func Test_LineageService_PublishRelease_Capabilities(t *testing.T) {
	Convey("Test lineageService PublishRelease with functions and Skills on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		bba := pmock.NewMockBknBackendAccess(mockCtrl)
		efa := pmock.NewMockExecutionFactoryAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, bba: bba, efa: efa, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"
		request := interfaces.PublishReleaseRequest{Branch: branch, Version: "1.0.0"}

		id := newLineageID()
		networkID := id + "_v1_0_0"
		live := capabilityDefinitions(id, "A part.")
		_, err := realGS.InitLineage(ctx, id, live, "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, branch, "")
		So(err, ShouldBeNil)
		_, err = realGS.CommitFiles(ctx, id, branch, capabilityDefinitions(id, "A part, edited."), "edit", author)
		So(err, ShouldBeNil)

		ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), networkID).Return(nil, false, nil)
		ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
		bba.EXPECT().ExportBKN(gomock.Any(), token, id).Return(live, nil)

		Convey("A function naming the old network is copied, an unchanged Skill reused, and both recorded\n", func() {
			var importedBox, importedPkg []byte
			var copyID string
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			efa.EXPECT().ExportToolbox(gomock.Any(), token, capBoxID).Return(capabilityExport(`KN_ID = "`+id+`"`), nil)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, capSkillID).Return(releasedSkill("Reads the bound network.\n"), nil)
			efa.EXPECT().ImportToolbox(gomock.Any(), token, gomock.Any(), interfaces.IMPEX_MODE_UPSERT).DoAndReturn(
				func(_ context.Context, _ string, data []byte, _ string) error {
					importedBox = data
					return nil
				})
			efa.EXPECT().ExportToolbox(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, boxID string) ([]byte, error) {
					copyID = boxID
					return importedBox, nil
				})
			var imported map[string][]byte
			bba.EXPECT().ImportBKN(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, files map[string][]byte) (*interfaces.BKNImportResult, error) {
					imported = files
					importedPkg = files["action_types/replenish.bkn"]
					return &interfaces.BKNImportResult{KNID: networkID}, nil
				})
			ra.EXPECT().CreateRelease(gomock.Any(), gomock.Any()).Return(nil)
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).DoAndReturn(
				func(context.Context, string, string) (map[string][]byte, error) { return imported, nil })

			result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			So(err, ShouldBeNil)
			// The copy reads the new network.
			So(string(importedBox), ShouldContainSubstring, `KN_ID = \"`+networkID+`\"`)
			So(copyID, ShouldNotEqual, capBoxID)

			// The new network calls the copy and binds the reused Skill.
			network, err := loadNetwork(imported)
			So(err, ShouldBeNil)
			So(network.ActionTypes[0].ActionSource.BoxID, ShouldEqual, copyID)
			So(network.ActionTypes[0].ActionSource.ToolID, ShouldNotEqual, capToolID)
			So(network.Capabilities.Skills[0], ShouldResemble, &bkn.BknCapabilitySkill{ID: capSkillID, Name: "plan-supply"})
			So(string(importedPkg), ShouldContainSubstring, "| const | "+networkID+" |")

			// main records both under the ids the new network uses.
			recorded, err := realGS.ReadCapabilities(ctx, id, "main")
			So(err, ShouldBeNil)
			So(recorded, ShouldContainKey, "functions/"+copyID+"/toolbox.json")
			So(recorded, ShouldNotContainKey, "functions/"+capBoxID+"/toolbox.json")
			So(recorded["skills/"+capSkillID+"/SKILL.md"], ShouldResemble, releasedSkill("Reads the bound network.\n")["SKILL.md"])
			manifest, err := capability.ParseManifest(recorded["release.yaml"])
			So(err, ShouldBeNil)
			So(manifest.Functions, ShouldHaveLength, 1)
			So(manifest.Functions[0].ID, ShouldEqual, copyID)
			So(manifest.Functions[0].Name, ShouldEqual, "supply_tools_"+strings.ReplaceAll(networkID, "-", "_"))
			So(manifest.Functions[0].Networks, ShouldResemble, []string{networkID})
			So(manifest.Skills[0].ID, ShouldEqual, capSkillID)
			So(manifest.Skills[0].Networks, ShouldResemble, []string{networkID})
			So(manifest.Skills[0].Digest, ShouldEqual, capability.Digest(releasedSkill("Reads the bound network.\n")))
			mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(result.Commit, ShouldEqual, mainHead)
			// The definitions are those of the new network, as exported.
			onMain, _ := realGS.ReadTree(ctx, id, "main")
			So(onMain, ShouldResemble, imported)
		})

		Convey("A Skill whose release was edited in place is drift, and nothing is tagged\n", func() {
			_, err := realGS.CommitCapabilities(ctx, id, "main", map[string][]byte{
				"skills/" + capSkillID + "/SKILL.md": releasedSkill("v1\n")["SKILL.md"],
				"release.yaml": []byte("skills:\n  - id: " + capSkillID + "\n    digest: " +
					capability.Digest(releasedSkill("v1\n")) + "\n"),
			}, "record", author)
			So(err, ShouldBeNil)
			// The sync merges main into the branch and brings main's capability entries along.
			synced, err := svc.SyncBranch(ctx, id, branch, author)
			So(err, ShouldBeNil)
			So(synced.Result, ShouldEqual, interfaces.SYNC_RESULT_MERGED)
			onBranch, _ := realGS.ReadCapabilities(ctx, id, branch)
			onMain, _ := realGS.ReadCapabilities(ctx, id, "main")
			So(onBranch, ShouldResemble, onMain)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, capSkillID).Return(releasedSkill("v2\n"), nil)

			_, err = svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_Drift)
			report := errorDetails(err).(*interfaces.DriftReport)
			So(report.Capabilities.Changed, ShouldResemble, []string{"skills/" + capSkillID + "/SKILL.md"})
			_, err = realGS.ResolveRef(ctx, id, "refs/tags/v1.0.0")
			So(err, ShouldNotBeNil)
		})

		Convey("A refused copy names the toolbox and records nothing\n", func() {
			mainBefore, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			bba.EXPECT().ExportBKN(gomock.Any(), token, networkID).Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			efa.EXPECT().ExportToolbox(gomock.Any(), token, capBoxID).Return(capabilityExport(`KN_ID = "`+id+`"`), nil)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, capSkillID).Return(releasedSkill("x\n"), nil)
			efa.EXPECT().ImportToolbox(gomock.Any(), token, gomock.Any(), gomock.Any()).Return(
				&interfaces.ExecutionFactoryError{HTTPCode: http.StatusForbidden})

			_, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Lineage_CapabilityRequestFailed)
			details := errorDetails(err).(map[string]any)
			So(details["kind"], ShouldEqual, capability.KIND_FUNCTION)
			So(details["id"], ShouldEqual, capBoxID)
			So(details["name"], ShouldEqual, "supply_tools")
			So(details["operation"], ShouldEqual, capability.OPERATION_COPY)
			mainAfter, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
			So(mainAfter, ShouldEqual, mainBefore)
		})
	})
}
