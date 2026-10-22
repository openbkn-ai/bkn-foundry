// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

type archiveAccessStub struct {
	records []*interfaces.ObjectMetricRecordV1
}

func (stub archiveAccessStub) ListObjectMetricArchive(context.Context, string, string) ([]*interfaces.ObjectMetricRecordV1, error) {
	return stub.records, nil
}
func (archiveAccessStub) ImportObjectMetricArchive(context.Context, *sql.Tx, string, string, []*interfaces.ObjectMetricRecordV1) error {
	return nil
}
func (archiveAccessStub) DeleteObjectMetricArchive(context.Context, *sql.Tx, string, string) error {
	return nil
}

type archivePermissionStub struct {
	interfaces.PermissionService
	visible map[string]bool
}

func (stub archivePermissionStub) FilterVisibleResources(_ context.Context, _ string, ids []string,
	_ []string) (map[string]interfaces.PermissionResourceOps, error) {
	result := make(map[string]interfaces.PermissionResourceOps, len(ids))
	for _, id := range ids {
		if stub.visible == nil || stub.visible[id] {
			result[id] = interfaces.PermissionResourceOps{ResourceID: id}
		}
	}
	return result, nil
}

func newTestBKNService(t *testing.T) (*bknService, *gomock.Controller, *bmock.MockKNService) {
	t.Helper()
	svc, mockCtrl, kns, cbs := newTestBKNServiceWithCapabilities(t)
	// The cases built on this helper are about the model, not the dependency section: nothing is
	// bound. The capability cases below set their own expectations, which is why the default
	// lives here rather than in the shared constructor — gomock takes the first match, so a
	// permissive default there would shadow them.
	cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
		Return(&interfaces.CapabilityBindingsList{}, nil).AnyTimes()
	return svc, mockCtrl, kns
}

// newTestBKNServiceWithCapabilities also hands back the capability binding service. Export reads
// the bindings to write the dependency section, so a test that exports has to say what is bound;
// the default here is "nothing", which is what the existing cases assume.
func newTestBKNServiceWithCapabilities(t *testing.T) (*bknService, *gomock.Controller,
	*bmock.MockKNService, *bmock.MockCapabilityBindingService) {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	kns := bmock.NewMockKNService(mockCtrl)
	cbs := bmock.NewMockCapabilityBindingService(mockCtrl)
	svc := &bknService{
		appSetting: &common.AppSetting{},
		kns:        kns,
		cbs:        cbs,
		ps:         archivePermissionStub{},
	}
	return svc, mockCtrl, kns, cbs
}

func Test_bknService_ExportToTar(t *testing.T) {
	Convey("Test bknService ExportToTar\n", t, func() {
		svc, mockCtrl, kns := newTestBKNService(t)
		defer mockCtrl.Finish()

		Convey("Success with empty KN (no sub-types)\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn1",
				KNName: "Test Network",
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)

			data, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)

			So(err, ShouldBeNil)
			So(len(data), ShouldBeGreaterThan, 0)
		})

		Convey("Success with KN containing all sub-types\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn2",
				KNName: "Full Network",
				ObjectTypes: []*interfaces.ObjectType{
					{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1", OTName: "OT1"}},
				},
				RelationTypes: []*interfaces.RelationType{
					{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt1", RTName: "RT1"}},
				},
				ActionTypes: []*interfaces.ActionType{
					{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at1", ATName: "AT1"}},
				},
				ConceptGroups: []*interfaces.ConceptGroup{
					{CGID: "cg1", CGName: "CG1"},
				},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)

			data, err := svc.ExportToTar(context.Background(), "kn2", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)

			So(err, ShouldBeNil)
			So(len(data), ShouldBeGreaterThan, 0)
		})

		Convey("Failed when GetKNByID returns error\n", func() {
			getErr := &rest.HTTPError{
				HTTPCode: http.StatusInternalServerError,
				Language: rest.DefaultLanguage,
				BaseError: rest.BaseError{
					ErrorCode: berrors.BknBackend_KnowledgeNetwork_InternalError,
				},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn-err", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(nil, getErr)

			data, err := svc.ExportToTar(context.Background(), "kn-err", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)

			So(err, ShouldNotBeNil)
			So(errors.Is(err, getErr), ShouldBeTrue)
			So(data, ShouldBeNil)
		})

		Convey("Export tar preserves object metric V1 versions readable by LoadNetworkFromTar\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn-metrics",
				KNName: "KN With Metrics",
				Branch: interfaces.MAIN_BRANCH,
				ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "pod", OTName: "Pod",
				}}},
			}
			svc.archive = archiveAccessStub{records: []*interfaces.ObjectMetricRecordV1{{
				KNID: "kn-metrics", Branch: interfaces.MAIN_BRANCH,
				Definition: interfaces.ObjectMetricDefinitionV1{
					ID: "pod_running_count", Version: 2, Code: "running_pods",
					Name: "Running Pods", OwnerObjectTypeID: "pod",
					MetricType: interfaces.ObjectMetricTypeAtomic,
					Lifecycle:  interfaces.ObjectMetricLifecycleV1{Status: "published"},
				},
			}}}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn-metrics", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)

			data, err := svc.ExportToTar(context.Background(), "kn-metrics", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
			So(err, ShouldBeNil)
			loaded, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil)
			So(len(loaded.ObjectMetrics), ShouldEqual, 1)
			var record interfaces.ObjectMetricRecordV1
			So(json.Unmarshal(loaded.ObjectMetrics[0], &record), ShouldBeNil)
			So(record.Definition.ID, ShouldEqual, "pod_running_count")
			So(record.Definition.Version, ShouldEqual, 2)
			So(record.Definition.Lifecycle.Status, ShouldEqual, "published")
		})
	})
}

func TestBKNServiceExportFiltersObjectMetricArchiveByMetricAndOwnerVisibility(t *testing.T) {
	svc, mockCtrl, kns := newTestBKNService(t)
	defer mockCtrl.Finish()
	svc.archive = archiveAccessStub{records: []*interfaces.ObjectMetricRecordV1{
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "visible", Version: 1, OwnerObjectTypeID: "owner-visible"}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "visible", Version: 2, OwnerObjectTypeID: "owner-visible"}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "metric-hidden", Version: 1, OwnerObjectTypeID: "owner-visible"}},
		{Definition: interfaces.ObjectMetricDefinitionV1{ID: "owner-hidden", Version: 1, OwnerObjectTypeID: "owner-hidden"}},
	}}
	svc.ps = archivePermissionStub{visible: map[string]bool{
		"kn-1/visible":      true,
		"kn-1/owner-hidden": true,
	}}
	kns.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(&interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "owner-visible", OTName: "Visible Owner",
		}}},
	}, nil)

	network, err := svc.buildNetwork(context.Background(), "kn-1", interfaces.MAIN_BRANCH)
	if err != nil {
		t.Fatal(err)
	}
	if len(network.ObjectMetrics) != 2 {
		t.Fatalf("exported object metrics = %d, want both visible versions", len(network.ObjectMetrics))
	}
	for index, raw := range network.ObjectMetrics {
		var record interfaces.ObjectMetricRecordV1
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Definition.ID != "visible" || record.Definition.Version != index+1 {
			t.Fatalf("exported metric[%d] = %+v", index, record.Definition)
		}
	}
}

// Test_bknService_ExportCapabilities covers the dependency section riding along with the model.
// An export that dropped it would move a network to another environment with its Skills and
// functions silently unbound, and nothing in the file to say they were ever there.
func Test_bknService_ExportCapabilities(t *testing.T) {
	Convey("导出带能力依赖声明", t, func() {
		svc, mockCtrl, kns, cbs := newTestBKNServiceWithCapabilities(t)
		defer mockCtrl.Finish()

		Convey("id 与名字双写", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
				Return(&interfaces.KN{KNID: "kn1", KNName: "供应链"}, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, q interfaces.CapabilityBindingsQueryParams) (*interfaces.CapabilityBindingsList, error) {
					// No paging: a page of the bindings would ship a subset of what the network
					// depends on, with nothing to say so.
					So(q.Limit, ShouldEqual, 0)
					return &interfaces.CapabilityBindingsList{MetadataAvailable: true,
						Entries: []*interfaces.CapabilityBinding{
							{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1", Name: "交期评估"},
							{ID: "b2", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1",
								CapabilityID: "tool-1", Name: "BOM 展开", OwnerName: "供应链计算"},
						}}, nil
				})

			data, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
			So(err, ShouldBeNil)

			network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil)
			So(network.Capabilities, ShouldNotBeNil)
			So(network.Capabilities.Skills[0].ID, ShouldEqual, "skill-1")
			So(network.Capabilities.Skills[0].Name, ShouldEqual, "交期评估")
			fn := network.Capabilities.Functions[0]
			So(fn.BoxID, ShouldEqual, "box-1")
			So(fn.ToolID, ShouldEqual, "tool-1")
			So(fn.BoxName, ShouldEqual, "供应链计算")
			So(fn.ToolName, ShouldEqual, "BOM 展开")
		})

		Convey("没有绑定时不写这一段", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
				Return(&interfaces.KN{KNID: "kn2", KNName: "空网络"}, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				Return(&interfaces.CapabilityBindingsList{}, nil)

			data, err := svc.ExportToTar(context.Background(), "kn2", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
			So(err, ShouldBeNil)
			So(string(data), ShouldNotContainSubstring, "capabilities")
		})

		Convey("执行工厂不可达时导出失败，不产出只剩 id 的段", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn3", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
				Return(&interfaces.KN{KNID: "kn3", KNName: "名字缺失"}, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				Return(&interfaces.CapabilityBindingsList{MetadataAvailable: false,
					Entries: []*interfaces.CapabilityBinding{
						{ID: "b5", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1"},
					}}, nil)

			data, err := svc.ExportToTar(context.Background(), "kn3", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
			So(err, ShouldNotBeNil)
			So(data, ShouldBeNil)
		})

		Convey("目标已消失的绑定不写进依赖段", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn4", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
				Return(&interfaces.KN{KNID: "kn4", KNName: "含悬空绑定"}, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				Return(&interfaces.CapabilityBindingsList{MetadataAvailable: true,
					Entries: []*interfaces.CapabilityBinding{
						{ID: "b3", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-live", Name: "在的"},
						{ID: "b4", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-gone",
							Status: interfaces.CAPABILITY_STATUS_MISSING},
					}}, nil)

			data, err := svc.ExportToTar(context.Background(), "kn4", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
			So(err, ShouldBeNil)
			network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil)
			So(len(network.Capabilities.Skills), ShouldEqual, 1)
			So(network.Capabilities.Skills[0].ID, ShouldEqual, "skill-live")
		})
	})
}

// Test_bknService_ExportMCPCapabilities pins the MCP half of the dependency section. An export
// that dropped it would move a network to another environment with its MCP tools silently
// unbound, and nothing in the file to say they were ever there.
func Test_bknService_ExportMCPCapabilities(t *testing.T) {
	Convey("导出带 MCP 依赖声明", t, func() {
		svc, mockCtrl, kns, cbs := newTestBKNServiceWithCapabilities(t)
		defer mockCtrl.Finish()

		kns.EXPECT().GetKNByID(gomock.Any(), "kn-mcp", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
			Return(&interfaces.KN{KNID: "kn-mcp", KNName: "带 MCP"}, nil)
		cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
			Return(&interfaces.CapabilityBindingsList{MetadataAvailable: true,
				Entries: []*interfaces.CapabilityBinding{
					{ID: "b6", CapabilityType: interfaces.CAPABILITY_TYPE_MCP_TOOL, OwnerID: "mcp-1",
						CapabilityID: "expedite", OwnerName: "供应链 MCP", Name: "expedite"},
				}}, nil)

		data, err := svc.ExportToTar(context.Background(), "kn-mcp", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
		So(err, ShouldBeNil)

		network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
		So(err, ShouldBeNil)
		So(network.Capabilities, ShouldNotBeNil)
		So(len(network.Capabilities.MCPTools), ShouldEqual, 1)
		tool := network.Capabilities.MCPTools[0]
		So(tool.MCPID, ShouldEqual, "mcp-1")
		So(tool.MCPName, ShouldEqual, "供应链 MCP")
		// The tool travels by name only: MCP has no separate id to fall back from.
		So(tool.ToolName, ShouldEqual, "expedite")
	})
}

// Test_bknService_ExportSkipsModelReferences keeps a reference from turning into a mount on the
// round trip. A capability that is in the listing because an object type or action type uses it
// has no binding of its own, and the model file already carries that object type or action type.
func Test_bknService_ExportSkipsModelReferences(t *testing.T) {
	Convey("导出只带显式挂载，不带模型引用而来的能力", t, func() {
		svc, mockCtrl, kns, cbs := newTestBKNServiceWithCapabilities(t)
		defer mockCtrl.Finish()

		kns.EXPECT().GetKNByID(gomock.Any(), "kn5", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
			Return(&interfaces.KN{KNID: "kn5", KNName: "含引用"}, nil)
		cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
			Return(&interfaces.CapabilityBindingsList{MetadataAvailable: true,
				Entries: []*interfaces.CapabilityBinding{
					{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
						CapabilityID: "mounted", Name: "挂了的"},
					// No id: nobody mounted it, an action type reaches for it.
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1",
						CapabilityID: "referenced", Name: "只被引用的"},
				}}, nil)

		data, err := svc.ExportToTar(context.Background(), "kn5", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
		So(err, ShouldBeNil)

		network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
		So(err, ShouldBeNil)
		So(len(network.Capabilities.Skills), ShouldEqual, 1)
		So(len(network.Capabilities.Functions), ShouldEqual, 0)
	})
}

func Test_bknService_DiffNetworks(t *testing.T) {
	Convey("Test bknService DiffNetworks\n", t, func() {
		svc, mockCtrl, kns := newTestBKNService(t)
		defer mockCtrl.Finish()

		baseKN := &interfaces.KN{
			KNID: "kn1", KNName: "供应链主网", Branch: "main",
			ObjectTypes: []*interfaces.ObjectType{{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "bom", OTName: "产品BOM"},
			}},
		}
		targetKN := &interfaces.KN{
			KNID: "kn2", KNName: "供应链主网（试点）", Branch: "main",
			ObjectTypes: []*interfaces.ObjectType{{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "bom", OTName: "产品物料清单"},
			}},
		}

		Convey("Both sides are loaded and the definition difference is reported\n", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", "main", interfaces.Mode_Export).Return(baseKN, nil)
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", "main", interfaces.Mode_Export).Return(targetKN, nil)

			result, err := svc.DiffNetworks(context.Background(), interfaces.KNDiffRequest{
				Base:   interfaces.KNRef{KNID: "kn1", Branch: "main"},
				Target: interfaces.KNRef{KNID: "kn2", Branch: "main"},
			})
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(result.Base.Name, ShouldEqual, "供应链主网")
			So(result.Target.Name, ShouldEqual, "供应链主网（试点）")
			So(result.Summary.Updated, ShouldEqual, 1)
			So(len(result.Entries), ShouldEqual, 1)
			So(result.Entries[0].ID, ShouldEqual, "bom")
			// The two networks carry different ids and names; that difference is reported on its
			// own rather than counted with the definitions.
			So(result.Network, ShouldNotBeNil)
			So(result.Network.Action, ShouldEqual, bknsdk.DiffUpdate)
			So(result.Lineage.CommonIDs, ShouldEqual, 1)
		})

		Convey("A caller who cannot read the base network is refused before the target is touched\n", func() {
			denied := rest.NewHTTPError(context.Background(), http.StatusForbidden, berrors.BknBackend_KnowledgeNetwork_InvalidParameter)
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", "main", interfaces.Mode_Export).Return(nil, denied)

			result, err := svc.DiffNetworks(context.Background(), interfaces.KNDiffRequest{
				Base:   interfaces.KNRef{KNID: "kn1", Branch: "main"},
				Target: interfaces.KNRef{KNID: "kn2", Branch: "main"},
			})
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

		Convey("Name fallback is passed through to the differ\n", func() {
			renamedID := &interfaces.KN{
				KNID: "kn2", KNName: "供应链主网（试点）", Branch: "main",
				ObjectTypes: []*interfaces.ObjectType{{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "0193-uuid", OTName: "产品BOM"},
				}},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", "main", interfaces.Mode_Export).Return(baseKN, nil)
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", "main", interfaces.Mode_Export).Return(renamedID, nil)

			result, err := svc.DiffNetworks(context.Background(), interfaces.KNDiffRequest{
				Base:           interfaces.KNRef{KNID: "kn1", Branch: "main"},
				Target:         interfaces.KNRef{KNID: "kn2", Branch: "main"},
				FallbackByName: true,
			})
			So(err, ShouldBeNil)
			So(len(result.Entries), ShouldEqual, 1)
			So(result.Entries[0].PairedBy, ShouldEqual, "name")
			So(result.Entries[0].BaseID, ShouldEqual, "bom")
			So(result.Entries[0].TargetID, ShouldEqual, "0193-uuid")
		})
	})
}

// misplacedKN has an action type whose parameter carries the value source in Source, as a legacy
// parameter table read by 2.x stores it.
func misplacedKN(knID string) *interfaces.KN {
	return &interfaces.KN{
		KNID:   knID,
		KNName: "错位参数",
		ActionTypes: []*interfaces.ActionType{{
			ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
				ATID: "restart_pod", ATName: "重启Pod",
				Parameters: []interfaces.Parameter{{Name: "pod", Type: "string", Source: "property", ValueFrom: "pod_name"}},
			},
		}},
	}
}

func Test_bknService_ExportSpec(t *testing.T) {
	Convey("Test bknService ExportToTar spec selection\n", t, func() {
		svc, mockCtrl, kns := newTestBKNService(t)
		defer mockCtrl.Finish()

		Convey("A network without 3.0 declarations is written as 2.x when nothing is asked for\n", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(misplacedKN("kn1"), nil)
			data, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_AUTO)
			So(err, ShouldBeNil)
			network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil)
			So(network.BknSpec, ShouldEqual, bknsdk.BknSpecLegacy)
		})

		Convey("2.0 is written as 2.x\n", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(misplacedKN("kn1"), nil)
			_, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_2)
			So(err, ShouldBeNil)
		})

		Convey("3.0 is refused while the network check finds misplaced parameters, and names them\n", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(misplacedKN("kn1"), nil)
			data, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_3)
			So(data, ShouldBeNil)
			var httpErr *rest.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_KnowledgeNetwork_ExportNeedsNetworkCheck)
			found := httpErr.BaseError.ErrorDetails.(*interfaces.NetworkCheckResult)
			So(found.MisplacedParameters, ShouldHaveLength, 1)
			So(found.MisplacedParameters[0].ActionTypeID, ShouldEqual, "restart_pod")
		})

		Convey("3.0 is written as 3.0 when the check finds nothing\n", func() {
			kn := misplacedKN("kn2")
			kn.ActionTypes[0].Parameters[0] = interfaces.Parameter{Name: "pod", Type: "string", Source: "Body", ValueFrom: "property", Value: "pod_name"}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)
			data, err := svc.ExportToTar(context.Background(), "kn2", interfaces.MAIN_BRANCH, interfaces.BKN_EXPORT_SPEC_3)
			So(err, ShouldBeNil)
			network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil) // the 3.0 export reads back under 3.0
			So(network.BknSpec, ShouldEqual, bknsdk.BknSpecV3)
		})
	})
}

func Test_bknService_CheckNetwork(t *testing.T) {
	Convey("Test bknService CheckNetwork\n", t, func() {
		svc, mockCtrl, kns := newTestBKNService(t)
		defer mockCtrl.Finish()

		kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(misplacedKN("kn1"), nil)
		result, err := svc.CheckNetwork(context.Background(), "kn1", interfaces.MAIN_BRANCH)
		So(err, ShouldBeNil)
		So(result.MisplacedParameters, ShouldResemble, []interfaces.NetworkCheckParameter{{
			ActionTypeID: "restart_pod",
			Parameter:    "pod",
			Current:      interfaces.NetworkCheckParameterValue{Source: "property", ValueFrom: "pod_name"},
			Suggested:    interfaces.NetworkCheckParameterValue{ValueFrom: "property", Value: "pod_name"},
		}})

		kns.EXPECT().GetKNByID(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(&interfaces.KN{KNID: "kn2"}, nil)
		result, err = svc.CheckNetwork(context.Background(), "kn2", interfaces.MAIN_BRANCH)
		So(err, ShouldBeNil)
		So(result.MisplacedParameters, ShouldBeEmpty)
		So(result.MisplacedParameters, ShouldNotBeNil) // an empty list, not null, in the response
	})
}
