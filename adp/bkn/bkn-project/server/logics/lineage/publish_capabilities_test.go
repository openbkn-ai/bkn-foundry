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

func Test_LineageService_PrepareRelease_Capabilities(t *testing.T) {
	Convey("Test lineageService PrepareRelease and PublishRelease with functions and Skills on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		kva := pmock.NewMockKNVersionAccess(mockCtrl)
		efa := pmock.NewMockExecutionFactoryAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, kva: kva, efa: efa, ra: ra}
		ctx := context.Background()
		const branch = "project/p1"
		request := interfaces.PublishReleaseRequest{Branch: branch, Version: "1.0.0"}

		id := newLineageID()
		live := capabilityDefinitions(id, "A part.")
		_, err := realGS.InitLineage(ctx, id, live, "init", author)
		So(err, ShouldBeNil)
		_, err = svc.CreateBranch(ctx, id, branch, "")
		So(err, ShouldBeNil)
		_, err = realGS.CommitFiles(ctx, id, branch, capabilityDefinitions(id, "A part, edited."), "edit", author)
		So(err, ShouldBeNil)
		// The function reads the lineage's network by its id.
		functionCode := `KN_ID = "` + id + `"`
		skillBody := "Reads the bound network.\n"

		// expectExport expects the function and the Skill read live.
		expectExport := func() {
			efa.EXPECT().ExportToolbox(gomock.Any(), token, capBoxID).Return(capabilityExport(functionCode), nil)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, capSkillID).Return(releasedSkill(skillBody), nil)
		}
		// expectCopy expects the function copied; it returns where the copy's import and id land.
		expectCopy := func() (*[]byte, *string) {
			var importedBox []byte
			var copyID string
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
			return &importedBox, &copyID
		}
		// expectWrite expects the draft written and the release recorded.
		expectWrite := func(revision int64) (*map[string][]byte, **interfaces.Release) {
			var written map[string][]byte
			var recorded *interfaces.Release
			kva.EXPECT().ReplaceDraft(gomock.Any(), token, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, files map[string][]byte, _ interfaces.KNReplaceImport) (*interfaces.BKNImportResult, int64, error) {
					written = files
					return &interfaces.BKNImportResult{KNID: id}, revision, nil
				})
			kva.EXPECT().BuildDraftIndex(gomock.Any(), token, id).Return(nil)
			ra.EXPECT().PrepareRelease(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, release *interfaces.Release) (bool, error) {
					recorded = release
					return true, nil
				})
			return &written, &recorded
		}
		// released10 records on main that 1.0.0 released the function and the Skill as they are live
		// now, except those named changed, recorded with the digest of an older content: they were
		// edited since and the edit committed to main. The branch syncs to it.
		released10 := func(changed ...string) []*interfaces.Release {
			fn, err := capability.NewFunction(capabilityExport(functionCode))
			So(err, ShouldBeNil)
			sk, err := capability.NewSkill(capSkillID, releasedSkill(skillBody))
			So(err, ShouldBeNil)
			digest := func(it *capability.Item, older string) string {
				for _, c := range changed {
					if c == it.ID {
						return older
					}
				}
				return it.Digest
			}
			oldFn, err := capability.NewFunction(capabilityExport(`KN_ID = "older"`))
			So(err, ShouldBeNil)
			manifest, err := (&capability.Manifest{
				Functions: []*capability.ManifestEntry{{ID: fn.ID, Name: fn.Name, Digest: digest(fn, oldFn.Digest),
					Networks: []string{copySuffix(id, "1.0.0")}}},
				Skills: []*capability.ManifestEntry{{ID: sk.ID, Name: sk.Name, Digest: digest(sk, capability.Digest(releasedSkill("Older.\n"))),
					Networks: []string{copySuffix(id, "1.0.0")}}},
			}).Marshal()
			So(err, ShouldBeNil)
			files := capability.Files([]*capability.Item{fn, sk})
			files["release.yaml"] = manifest
			_, err = realGS.CommitCapabilities(ctx, id, "main", files, "record 1.0.0", author)
			So(err, ShouldBeNil)
			synced, err := svc.SyncBranch(ctx, id, branch, author)
			So(err, ShouldBeNil)
			So(synced.Result, ShouldEqual, interfaces.SYNC_RESULT_MERGED)
			return []*interfaces.Release{{LineageID: id, NetworkID: id, Version: "1.0.0", State: interfaces.RELEASE_STATE_PUBLISHED}}
		}
		// expectedCopyID is the id the copy of the function for version gets: it follows
		// copySuffix(lineage, version), never the network.
		expectedCopyID := func(version string) string {
			fn, err := capability.NewFunction(capabilityExport(functionCode))
			So(err, ShouldBeNil)
			copies, err := capability.Preview([]*capability.Step{{Item: fn, Copy: true}}, copySuffix(id, "1.0.0"), copySuffix(id, version))
			So(err, ShouldBeNil)
			return copies[0].NewID
		}

		Convey("The first release reuses a function that names the network and an unchanged Skill, and publishing records both\n", func() {
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			expectExport()
			// No ImportToolbox: nothing is copied.
			written, recorded := expectWrite(8)

			release, err := svc.PrepareRelease(ctx, token, id, request, publisher)

			So(err, ShouldBeNil)
			// The draft is the lineage's network and keeps the original ids and its network id.
			network, err := loadNetwork(*written)
			So(err, ShouldBeNil)
			So(network.ID, ShouldEqual, id)
			So(network.ActionTypes[0].ActionSource.BoxID, ShouldEqual, capBoxID)
			So(network.ActionTypes[0].ActionSource.ToolID, ShouldEqual, capToolID)
			So(network.Capabilities.Skills[0], ShouldResemble, &bkn.BknCapabilitySkill{ID: capSkillID, Name: "plan-supply"})
			So(string((*written)["action_types/replenish.bkn"]), ShouldContainSubstring, "| const | "+id+" |")

			// The release keeps both under their own ids, for publishing to record on main.
			So(release, ShouldEqual, *recorded)
			So(release.Capabilities, ShouldContainKey, "functions/"+capBoxID+"/toolbox.json")
			So(string(release.Capabilities["functions/"+capBoxID+"/toolbox.json"]), ShouldContainSubstring, `KN_ID = \"`+id+`\"`)
			So(release.Capabilities["skills/"+capSkillID+"/SKILL.md"], ShouldResemble, releasedSkill(skillBody)["SKILL.md"])
			manifest, err := capability.ParseManifest(release.Capabilities["release.yaml"])
			So(err, ShouldBeNil)
			So(manifest.Functions, ShouldHaveLength, 1)
			So(manifest.Functions[0].ID, ShouldEqual, capBoxID)
			So(manifest.Functions[0].Name, ShouldEqual, "supply_tools")
			So(manifest.Skills[0].ID, ShouldEqual, capSkillID)
			So(manifest.Skills[0].Digest, ShouldEqual, capability.Digest(releasedSkill(skillBody)))
			// main records nothing until the release is published.
			onMain, err := realGS.ReadCapabilities(ctx, id, "main")
			So(err, ShouldBeNil)
			So(onMain, ShouldNotContainKey, "functions/"+capBoxID+"/toolbox.json")

			Convey("and publishing commits them to main after the running version's definitions\n", func() {
				ra.EXPECT().GetRelease(gomock.Any(), id, "1.0.0").Return(release, true, nil)
				kva.EXPECT().GetVersioning(gomock.Any(), token, id).Return(versioningAt(1, 8, true), nil)
				kva.EXPECT().Publish(gomock.Any(), token, id, gomock.Any()).Return(&interfaces.KNVersionPublished{KNVersion: interfaces.KNVersion{Version: 2}}, nil)
				ra.EXPECT().MarkPublished(gomock.Any(), release.ID, 2, gomock.Any(), gomock.Any()).Return(nil)
				kva.EXPECT().ExportVersion(gomock.Any(), token, id, interfaces.KN_VERSION_RUNNING).Return(*written, nil)

				result, err := svc.PublishRelease(ctx, token, id, request, publisher, author)

				So(err, ShouldBeNil)
				mainHead, _ := realGS.ResolveRef(ctx, id, "refs/heads/main")
				So(result.Commit, ShouldEqual, mainHead)
				recordedOnMain, err := realGS.ReadCapabilities(ctx, id, "main")
				So(err, ShouldBeNil)
				So(recordedOnMain, ShouldContainKey, "functions/"+capBoxID+"/toolbox.json")
				So(recordedOnMain["release.yaml"], ShouldResemble, release.Capabilities["release.yaml"])
				// The definitions are those of the running version, as exported.
				definitionsOnMain, _ := realGS.ReadTree(ctx, id, "main")
				So(definitionsOnMain, ShouldResemble, *written)
			})
		})

		Convey("A function changed since the previous release is copied under an id of the version, still naming the lineage's network\n", func() {
			latest := released10(capBoxID)
			expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 7, false), nil, latest)
			expectExport()
			importedBox, copyID := expectCopy()
			written, _ := expectWrite(8)

			req := request
			req.Version = "1.1.0"
			release, err := svc.PrepareRelease(ctx, token, id, req, publisher)

			So(err, ShouldBeNil)
			So(release.BaseVersion, ShouldEqual, "1.0.0")
			So(*copyID, ShouldEqual, expectedCopyID("1.1.0"))
			So(*copyID, ShouldNotEqual, capBoxID)
			// The copy's code still reads the lineage's network, not the copy key.
			So(string(*importedBox), ShouldContainSubstring, `KN_ID = \"`+id+`\"`)
			So(string(*importedBox), ShouldNotContainSubstring, copySuffix(id, "1.1.0")+`\"`)

			// The draft calls the copy and still passes the lineage's network id.
			network, err := loadNetwork(*written)
			So(err, ShouldBeNil)
			So(network.ID, ShouldEqual, id)
			So(network.ActionTypes[0].ActionSource.BoxID, ShouldEqual, *copyID)
			So(network.ActionTypes[0].ActionSource.ToolID, ShouldNotEqual, capToolID)
			So(network.Capabilities.Skills[0].ID, ShouldEqual, capSkillID)
			action := string((*written)["action_types/replenish.bkn"])
			So(action, ShouldContainSubstring, "| const | "+id+" |")
			So(action, ShouldNotContainSubstring, copySuffix(id, "1.1.0"))

			manifest, err := capability.ParseManifest(release.Capabilities["release.yaml"])
			So(err, ShouldBeNil)
			So(manifest.Functions[0].ID, ShouldEqual, *copyID)
			So(manifest.Functions[0].Name, ShouldEqual, "supply_tools_"+strings.ReplaceAll(copySuffix(id, "1.1.0"), "-", "_"))
			So(manifest.Skills[0].ID, ShouldEqual, capSkillID)

			Convey("and another version from the same base copies it under another id\n", func() {
				expectChecks(kva, ra, id, "1.2.0", versioningAt(2, 8, true), nil, latest)
				ra.EXPECT().ListReleases(gomock.Any(), preparedQuery(id)).Return([]*interfaces.Release{release}, nil)
				expectExport()
				_, second := expectCopy()
				_, _ = expectWrite(9)

				req.Version = "1.2.0"
				_, err := svc.PrepareRelease(ctx, token, id, req, publisher)

				So(err, ShouldBeNil)
				So(*second, ShouldEqual, expectedCopyID("1.2.0"))
				So(*second, ShouldNotEqual, *copyID)
				So(*second, ShouldNotEqual, capBoxID)
			})
		})

		Convey("A Skill whose release was edited in place is drift, and nothing is written\n", func() {
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
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			efa.EXPECT().GetReleasedSkill(gomock.Any(), token, capSkillID).Return(releasedSkill("v2\n"), nil)

			_, err = svc.PrepareRelease(ctx, token, id, request, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Release_Drift)
			report := errorDetails(err).(*interfaces.DriftReport)
			So(report.Capabilities.Changed, ShouldResemble, []string{"skills/" + capSkillID + "/SKILL.md"})
		})

		// flowStyle commits definitions whose Skill binding cannot be rewritten line by line.
		flowStyle := func() {
			flow := capabilityDefinitions(id, "A part, edited.")
			flow["network.bkn"] = []byte(strings.Replace(networkText, "name: Supply chain\n---",
				"name: Supply chain\ncapabilities: {skills: [{id: "+capSkillID+", name: plan-supply}]}\n---", 1))
			_, err := realGS.CommitFiles(ctx, id, branch, flow, "flow style", author)
			So(err, ShouldBeNil)
		}

		Convey("References a changed Skill's copy cannot reach reply 400 before anything is copied or written\n", func() {
			latest := released10(capSkillID)
			flowStyle()
			expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 7, false), nil, latest)
			expectExport()

			req := request
			req.Version = "1.1.0"
			_, err := svc.PrepareRelease(ctx, token, id, req, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidDefinitions)
		})

		Convey("A refused copy names the toolbox and writes nothing\n", func() {
			latest := released10(capBoxID)
			expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 7, false), nil, latest)
			expectExport()
			efa.EXPECT().ImportToolbox(gomock.Any(), token, gomock.Any(), gomock.Any()).Return(
				&interfaces.ExecutionFactoryError{HTTPCode: http.StatusForbidden})

			req := request
			req.Version = "1.1.0"
			_, err := svc.PrepareRelease(ctx, token, id, req, publisher)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Lineage_CapabilityRequestFailed)
			details := errorDetails(err).(map[string]any)
			So(details["kind"], ShouldEqual, capability.KIND_FUNCTION)
			So(details["id"], ShouldEqual, capBoxID)
			So(details["name"], ShouldEqual, "supply_tools")
			So(details["operation"], ShouldEqual, capability.OPERATION_COPY)
		})

		Convey("A preview lists what preparing would do with each function and Skill, and copies nothing\n", func() {
			expectChecks(kva, ra, id, "1.0.0", versioningAt(1, 7, false), nil, nil)
			expectExport()
			// No ImportToolbox or ReplaceDraft: the mocks expect none.

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.0.0")

			So(err, ShouldBeNil)
			So(preview.Refusals, ShouldBeEmpty)
			// Naming the network is no reason to copy: the network keeps its id.
			So(preview.Capabilities, ShouldResemble, []interfaces.CapabilityPlan{
				{Kind: "function", ID: capBoxID, Name: "supply_tools", Copy: false, Reasons: []string{}},
				{Kind: "skill", ID: capSkillID, Name: "plan-supply", Copy: false, Reasons: []string{}},
			})

			Convey("and a function changed since the previous release as copied\n", func() {
				latest := released10(capBoxID)
				expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 7, false), nil, latest)
				expectExport()

				preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.1.0")

				So(err, ShouldBeNil)
				So(preview.Refusals, ShouldBeEmpty)
				So(preview.Capabilities, ShouldResemble, []interfaces.CapabilityPlan{
					{Kind: "function", ID: capBoxID, Name: "supply_tools", Copy: true, Reasons: []string{capability.REASON_CHANGED}},
					{Kind: "skill", ID: capSkillID, Name: "plan-supply", Copy: false, Reasons: []string{}},
				})
			})
		})

		Convey("A preview lists references a changed Skill's copy cannot reach as a refusal\n", func() {
			latest := released10(capSkillID)
			flowStyle()
			expectChecks(kva, ra, id, "1.1.0", versioningAt(2, 7, false), nil, latest)
			expectExport()

			preview, err := svc.PreviewRelease(ctx, token, id, branch, "1.1.0")

			So(err, ShouldBeNil)
			So(refusalCodes(preview), ShouldResemble, []string{perrors.BknProject_Lineage_InvalidDefinitions})
			So(preview.Capabilities, ShouldBeEmpty)
		})
	})
}
