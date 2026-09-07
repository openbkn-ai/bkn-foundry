// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability_binding

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"bkn-backend/interfaces"
)

func srcOf(entry *interfaces.CapabilityBinding, kind string) *interfaces.CapabilitySource {
	for _, source := range entry.Sources {
		if source.Kind == kind {
			return source
		}
	}
	return nil
}

// TestApplyProvenance covers #1360: the workspace has to say why a capability is in the network,
// and a tool an object type or action type reaches for is in the network whether or not anyone
// mounted it.
func TestApplyProvenance(t *testing.T) {
	Convey("能力来源标注", t, func() {
		key := capabilityKey{
			capabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
			ownerID:        "box-1", capabilityID: "tool-1",
		}
		usedByModel := &provenance{available: true, byCapability: map[capabilityKey][]*interfaces.CapabilitySource{
			key: {{
				Kind: interfaces.CAPABILITY_SOURCE_OBJECT_TYPE,
				Refs: []*interfaces.CapabilitySourceRef{{ID: "ot_order", Name: "订单", Property: "risk_score"}},
			}},
		}}
		query := interfaces.CapabilityBindingsQueryParams{KNID: "kn1", Branch: "main"}

		Convey("显式挂载且被引用时是一行，两个来源都在", func() {
			entries := []*interfaces.CapabilityBinding{{
				ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
				OwnerID: "box-1", CapabilityID: "tool-1",
			}}

			out, total := applyProvenance(entries, 1, usedByModel, query)

			So(len(out), ShouldEqual, 1)
			So(total, ShouldEqual, 1)
			So(srcOf(out[0], interfaces.CAPABILITY_SOURCE_MANUAL), ShouldNotBeNil)
			objectType := srcOf(out[0], interfaces.CAPABILITY_SOURCE_OBJECT_TYPE)
			So(objectType, ShouldNotBeNil)
			// The property, not just the object type: dropping one is not dropping the other.
			So(objectType.Refs[0].Property, ShouldEqual, "risk_score")
		})

		Convey("只被引用、没挂载的也在列表里，且没有 manual 来源", func() {
			out, total := applyProvenance(nil, 0, usedByModel, query)

			So(len(out), ShouldEqual, 1)
			So(total, ShouldEqual, 1)
			So(out[0].CapabilityID, ShouldEqual, "tool-1")
			So(srcOf(out[0], interfaces.CAPABILITY_SOURCE_MANUAL), ShouldBeNil)
			So(srcOf(out[0], interfaces.CAPABILITY_SOURCE_OBJECT_TYPE), ShouldNotBeNil)
			// No row, so no id: that is what tells the caller it cannot be released here.
			So(out[0].ID, ShouldBeEmpty)
		})

		Convey("整箱展开的行标成 box 而不是 manual", func() {
			entries := []*interfaces.CapabilityBinding{{
				ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
				OwnerID: "box-1", CapabilityID: "tool-1", BoundAsBox: true,
			}}

			out, _ := applyProvenance(entries, 1, usedByModel, query)

			So(srcOf(out[0], interfaces.CAPABILITY_SOURCE_BOX), ShouldNotBeNil)
			So(srcOf(out[0], interfaces.CAPABILITY_SOURCE_MANUAL), ShouldBeNil)
		})

		Convey("类型过滤同样作用于补进来的条目", func() {
			skillQuery := query
			skillQuery.CapabilityType = interfaces.CAPABILITY_TYPE_SKILL

			out, total := applyProvenance(nil, 0, usedByModel, skillQuery)

			So(len(out), ShouldEqual, 0)
			So(total, ShouldEqual, 0)
		})

		Convey("翻页时不重复补，只有最后一页带", func() {
			paged := query
			paged.Offset = 10
			paged.Limit = 10

			out, total := applyProvenance(nil, 0, usedByModel, paged)

			So(len(out), ShouldEqual, 0)
			So(total, ShouldEqual, 0)
		})
	})
}

// TestActionSourceKey pins how an action type's executor maps onto a capability. An action type
// reaches a tool through a tool box or through an MCP server, and those are different capability
// types — the same split the bindings make.
func TestActionSourceKey(t *testing.T) {
	Convey("行动类的执行来源映射到能力身份", t, func() {
		Convey("工具箱工具", func() {
			key := actionSourceKey(interfaces.ActionSource{Type: "tool", BoxID: "box-1", ToolID: "t1"})
			So(key.capabilityType, ShouldEqual, interfaces.CAPABILITY_TYPE_FUNCTION)
			So(key.ownerID, ShouldEqual, "box-1")
			So(key.capabilityID, ShouldEqual, "t1")
		})

		Convey("MCP 工具按名字定位", func() {
			key := actionSourceKey(interfaces.ActionSource{Type: "mcp", McpID: "mcp-1", ToolName: "expedite"})
			So(key.capabilityType, ShouldEqual, interfaces.CAPABILITY_TYPE_MCP_TOOL)
			So(key.ownerID, ShouldEqual, "mcp-1")
			So(key.capabilityID, ShouldEqual, "expedite")
		})

		Convey("残缺的来源不产生身份", func() {
			So(actionSourceKey(interfaces.ActionSource{Type: "tool", BoxID: "box-1"}).empty(), ShouldBeTrue)
			So(actionSourceKey(interfaces.ActionSource{Type: "mcp", McpID: "mcp-1"}).empty(), ShouldBeTrue)
		})
	})
}
