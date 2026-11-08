// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// kineticDomain is one made-up business: its documents (a business requirement, a standard, a
// Skill document and an API description), its confirmed model, and what an analysis of it returns.
type kineticDomain struct {
	name     string
	docs     []*interfaces.RequirementDocument
	objects  []*interfaces.DraftObjectType
	proposed []agentLogicNeed
	// The object type and properties the metric rests on, and the names of the needs by kind.
	metricOn                            interfaces.RequirementPropertyRef
	metric, skill, api, mcp, actionType string
}

func kineticDomains() []kineticDomain {
	return []kineticDomain{
		{
			name: "outpatient clinic",
			docs: []*interfaces.RequirementDocument{
				doc("clinic-prd", interfaces.REQUIREMENT_DOC_PRD,
					"Front desk staff see which patients waited too long and move their appointments."),
				doc("clinic-std", interfaces.REQUIREMENT_DOC_STANDARD,
					"A patient waited too long when seen more than 30 minutes after the booked time."),
				doc("clinic-skill", interfaces.REQUIREMENT_DOC_SKILL,
					"The Triage priority Skill orders waiting patients by urgency and wait."),
				doc("clinic-api", interfaces.REQUIREMENT_DOC_API,
					"POST /appointments/{id}/reschedule moves an appointment to a new slot."),
			},
			objects: []*interfaces.DraftObjectType{
				{ID: "appointment", Name: "Appointment", Adopted: true, Properties: []*interfaces.DraftProperty{
					{Name: "booked_at", Type: "datetime"}, {Name: "seen_at", Type: "datetime"}}},
				{ID: "patient", Name: "Patient", Adopted: true},
			},
			proposed: []agentLogicNeed{
				{ID: "m", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Long wait rate",
					Definition:    "appointments seen more than 30 minutes after the booked time over all appointments",
					ObjectTypeIDs: []string{"appointment"}, Properties: []agentPropertyRef{{ObjectTypeID: "appointment", Property: "seen_at"}},
					SuggestedMetricType: "derived", Citations: []agentCitation{cite("clinic-std", "more than 30 minutes after the booked time")}},
				{ID: "s", Kind: interfaces.LOGIC_NEED_SKILL, Name: "Triage priority", ObjectTypeIDs: []string{"patient"},
					Citations: []agentCitation{cite("clinic-skill", "orders waiting patients by urgency and wait")}},
				{ID: "a", Kind: interfaces.LOGIC_NEED_API, Name: "Reschedule appointment", ObjectTypeIDs: []string{"appointment"},
					Citations: []agentCitation{cite("clinic-api", "moves an appointment to a new slot")}},
				{ID: "t", Kind: interfaces.LOGIC_NEED_MCP_TOOL, Name: "Patient messaging", SupplementReason: "patients are told of the new slot"},
				{ID: "x", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Move appointment", ObjectTypeIDs: []string{"appointment"},
					ExecutedBy: "a", Citations: []agentCitation{cite("clinic-prd", "move their appointments")}},
			},
			metricOn: interfaces.RequirementPropertyRef{ObjectTypeID: "appointment", Property: "seen_at"},
			metric:   "Long wait rate", skill: "Triage priority", api: "Reschedule appointment", mcp: "Patient messaging",
			actionType: "Move appointment",
		},
		{
			name: "cold-chain logistics",
			docs: []*interfaces.RequirementDocument{
				doc("cold-prd", interfaces.REQUIREMENT_DOC_PRD, "调度员要找出温度超限的运单，并把它们转给备用承运商。"),
				doc("cold-std", interfaces.REQUIREMENT_DOC_STANDARD, "冷藏运单在途温度高于 8 摄氏度即为超限。"),
				doc("cold-skill", interfaces.REQUIREMENT_DOC_SKILL, "温控异常研判 Skill 根据温度曲线判断异常原因。"),
				doc("cold-api", interfaces.REQUIREMENT_DOC_API, "承运商调度工具 transfer_shipment 把运单转给指定承运商。"),
			},
			objects: []*interfaces.DraftObjectType{
				{ID: "shipment", Name: "运单", Adopted: true, Properties: []*interfaces.DraftProperty{
					{Name: "max_temperature", Type: "decimal"}, {Name: "carrier_code", Type: "string"}}},
				{ID: "carrier", Name: "承运商", Adopted: true},
			},
			proposed: []agentLogicNeed{
				{ID: "m", Kind: interfaces.LOGIC_NEED_METRIC, Name: "温度超限运单占比",
					Definition:    "在途温度高于 8 摄氏度的冷藏运单数除以冷藏运单总数",
					ObjectTypeIDs: []string{"shipment"}, Properties: []agentPropertyRef{{ObjectTypeID: "shipment", Property: "max_temperature"}},
					SuggestedMetricType: "derived", Citations: []agentCitation{cite("cold-std", "在途温度高于 8 摄氏度即为超限")}},
				{ID: "s", Kind: interfaces.LOGIC_NEED_SKILL, Name: "温控异常研判", ObjectTypeIDs: []string{"shipment"},
					Citations: []agentCitation{cite("cold-skill", "根据温度曲线判断异常原因")}},
				{ID: "a", Kind: interfaces.LOGIC_NEED_API, Name: "运单查询接口", SupplementReason: "转单前要读运单现状"},
				{ID: "t", Kind: interfaces.LOGIC_NEED_MCP_TOOL, Name: "承运商调度工具", ObjectTypeIDs: []string{"carrier"},
					Citations: []agentCitation{cite("cold-api", "把运单转给指定承运商")}},
				// Carried out by the MCP tool, named by the Agent's own id.
				{ID: "x", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "转给备用承运商", ObjectTypeIDs: []string{"shipment", "carrier"},
					ExecutedBy: "t", Citations: []agentCitation{cite("cold-prd", "把它们转给备用承运商")}},
			},
			metricOn: interfaces.RequirementPropertyRef{ObjectTypeID: "shipment", Property: "max_temperature"},
			metric:   "温度超限运单占比", skill: "温控异常研判", api: "运单查询接口", mcp: "承运商调度工具", actionType: "转给备用承运商",
		},
		{
			name: "insurance claims",
			docs: []*interfaces.RequirementDocument{
				doc("claim-prd", interfaces.REQUIREMENT_DOC_PRD,
					"Adjusters see claims open past the service level and escalate them to a senior adjuster."),
				doc("claim-std", interfaces.REQUIREMENT_DOC_POLICY,
					"A claim is past the service level when open for more than 15 working days."),
				doc("claim-skill", interfaces.REQUIREMENT_DOC_SKILL,
					"The Fraud signal review Skill lists the signals a claim shows."),
				doc("claim-api", interfaces.REQUIREMENT_DOC_API,
					"POST /claims/{id}/escalate assigns a claim to a senior adjuster."),
			},
			objects: []*interfaces.DraftObjectType{
				{ID: "claim", Name: "Claim", Adopted: true, Properties: []*interfaces.DraftProperty{
					{Name: "opened_on", Type: "date"}, {Name: "status", Type: "string"}}},
				{ID: "adjuster", Name: "Adjuster", Adopted: true},
			},
			proposed: []agentLogicNeed{
				{ID: "m", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Overdue claim count",
					Definition:    "open claims opened more than 15 working days ago",
					ObjectTypeIDs: []string{"claim"}, Properties: []agentPropertyRef{{ObjectTypeID: "claim", Property: "opened_on"}},
					SuggestedMetricType: "atomic", Citations: []agentCitation{cite("claim-std", "open for more than 15 working days")}},
				{ID: "s", Kind: interfaces.LOGIC_NEED_SKILL, Name: "Fraud signal review", ObjectTypeIDs: []string{"claim"},
					Citations: []agentCitation{cite("claim-skill", "lists the signals a claim shows")}},
				{ID: "a", Kind: interfaces.LOGIC_NEED_API, Name: "Escalate claim", ObjectTypeIDs: []string{"claim"},
					Citations: []agentCitation{cite("claim-api", "assigns a claim to a senior adjuster")}},
				{ID: "t", Kind: interfaces.LOGIC_NEED_MCP_TOOL, Name: "Adjuster calendar", ObjectTypeIDs: []string{"adjuster"},
					SupplementReason: "the senior adjuster's free time decides who takes it"},
				{ID: "x", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Escalate to senior adjuster",
					ObjectTypeIDs: []string{"claim", "adjuster"}, ExecutedBy: "a",
					Citations: []agentCitation{cite("claim-prd", "escalate them to a senior adjuster")}},
			},
			metricOn: interfaces.RequirementPropertyRef{ObjectTypeID: "claim", Property: "opened_on"},
			metric:   "Overdue claim count", skill: "Fraud signal review", api: "Escalate claim", mcp: "Adjuster calendar",
			actionType: "Escalate to senior adjuster",
		},
	}
}

// Test_KineticAcrossDomains runs the kinetic analysis of #2953 over made-up businesses that share
// nothing with each other or with the sample data: every kind of need is kept with its basis, placed
// on the model, an action type names the API or MCP tool that carries it out, and accepted needs
// keep their ids and choices through another analysis.
func Test_KineticAcrossDomains(t *testing.T) {
	for _, d := range kineticDomains() {
		Convey("Kinetic needs of "+d.name+"\n", t, func() {
			docs := documentsByID(d.docs)
			draft := &interfaces.Draft{Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{ObjectTypes: d.objects,
				Confirmation: &interfaces.DraftConfirmation{Commit: "c"}}}
			var rejected []string
			reject := func(kind, text, reason string) { rejected = append(rejected, kind+" "+text+" "+reason) }
			byName := func(spec *interfaces.RequirementSpec) map[string]*interfaces.RequirementLogicNeed {
				out := map[string]*interfaces.RequirementLogicNeed{}
				for _, n := range spec.LogicNeeds {
					out[n.Name] = n
				}
				return out
			}

			// Skill documents and API descriptions are document types of their own.
			for _, doc := range d.docs {
				So(interfaces.IsRequirementDocType(doc.DocType), ShouldBeTrue)
			}

			spec := emptySpec()
			addLogicNeeds(spec, d.proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, map[string]string{}, reject)

			So(rejected, ShouldBeEmpty)
			needs := byName(spec)
			So(needs, ShouldHaveLength, 5)
			metric, actionType := needs[d.metric], needs[d.actionType]
			So(metric.Kind, ShouldEqual, interfaces.LOGIC_NEED_METRIC)
			So(metric.ObjectTypeIDs, ShouldResemble, []string{d.metricOn.ObjectTypeID})
			So(metric.Properties, ShouldResemble, []interfaces.RequirementPropertyRef{d.metricOn})
			So(metric.Citations, ShouldHaveLength, 1)
			So(docs[metric.Citations[0].DocumentID].DocType, ShouldBeIn, interfaces.REQUIREMENT_DOC_STANDARD,
				interfaces.REQUIREMENT_DOC_POLICY)
			// The Skill and the API quote the documents that describe them.
			So(docs[needs[d.skill].Citations[0].DocumentID].DocType, ShouldEqual, interfaces.REQUIREMENT_DOC_SKILL)
			if cited := needs[d.api].Citations; len(cited) > 0 {
				So(docs[cited[0].DocumentID].DocType, ShouldEqual, interfaces.REQUIREMENT_DOC_API)
			}
			executor := needs[d.api]
			if d.proposed[4].ExecutedBy == "t" {
				executor = needs[d.mcp]
			}
			So(actionType.ExecutedBy, ShouldEqual, executor.ID)
			So(interfaces.CanExecute(executor.Kind), ShouldBeTrue)

			// A person accepts the action type and what carries it out; the analysis runs again.
			actionType.Accepted, executor.Accepted = true, true
			ids := map[string]string{d.actionType: actionType.ID, executor.Name: executor.ID, d.metric: metric.ID}
			keepForKinetic(spec, false)
			So(byName(spec), ShouldHaveLength, 2)
			rejected = nil
			addLogicNeeds(spec, d.proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, map[string]string{}, reject)

			So(rejected, ShouldBeEmpty)
			again := byName(spec)
			So(again, ShouldHaveLength, 5)
			So(again[d.actionType].ID, ShouldEqual, ids[d.actionType])
			So(again[executor.Name].ID, ShouldEqual, ids[executor.Name])
			So(again[d.actionType].ExecutedBy, ShouldEqual, ids[executor.Name])
			// One no one accepted is analysed anew, under an id never given before.
			So(again[d.metric].ID, ShouldNotEqual, ids[d.metric])
		})
	}
}
