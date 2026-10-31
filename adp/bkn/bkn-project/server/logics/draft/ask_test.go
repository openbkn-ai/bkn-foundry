// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// Ask AI is tested on drafts of three domains: staff and departments, shipments and stations,
// clinic visits and patients.

func draftProp(name string, typ string, description string) *interfaces.DraftProperty {
	return &interfaces.DraftProperty{Name: name, Type: typ, MappedField: name, Description: description}
}

// staffDraft holds employees, still without an identity, and the departments they belong to.
func staffDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{{
				ID: "employees", Name: "Employees", Description: "People on the payroll", NeedsIdentity: true,
				Table: interfaces.DraftTable{ResourceID: "r_employees", Name: "employees", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{
					draftProp("emp_no", "string", ""), draftProp("dept_code", "string", ""),
					draftProp("full_name", "string", ""), draftProp("salary", "decimal", "Monthly, before tax"),
				},
				Key: interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_NONE},
			}, {
				ID: "departments", Name: "Departments", Adopted: true, PrimaryKeys: []string{"code"}, DisplayKey: "code",
				Table:      interfaces.DraftTable{ResourceID: "r_departments", Name: "departments", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{draftProp("code", "string", ""), draftProp("title", "string", "")},
				Key:        interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_PRIMARY_KEY},
			}},
			RelationTypes: []*interfaces.DraftRelationType{{
				ID: "employees_to_departments", Name: "Department of employee", Source: "employees", Target: "departments",
				Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping:     []interfaces.DraftFieldMapping{{SourceProperty: "dept_code", TargetProperty: "code"}},
				Evidence:    interfaces.DraftJoinEvidence{Computed: true, Rows: 20, Hits: 19, HitRate: 0.95},
			}, {
				ID: "departments_parent", Name: "Parent department", Source: "departments", Target: "departments",
				Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping:     []interfaces.DraftFieldMapping{{SourceProperty: "code", TargetProperty: "code"}},
			}},
		},
	}
}

func employeesTable() *interfaces.VegaTable {
	return &interfaces.VegaTable{ID: "r_employees", Name: "employees", Columns: []interfaces.VegaColumn{
		{Name: "emp_no", Type: "string"}, {Name: "dept_code", Type: "string"}, {Name: "full_name", Type: "string"},
		{Name: "salary", Type: "decimal"},
	}}
}

// shipmentDraft holds shipments joined to the station they leave from, and to the station they go
// to by a second relation.
func shipmentDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{{
				ID: "shipments", Name: "Shipments", Description: "Parcels in transit", Adopted: true, PrimaryKeys: []string{"waybill"}, DisplayKey: "waybill",
				Table: interfaces.DraftTable{ResourceID: "r_shipments", Name: "shipments", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{draftProp("waybill", "string", ""),
					draftProp("origin_station", "integer", ""), draftProp("via_station", "integer", ""),
					draftProp("dest_station", "integer", ""), draftProp("weight", "decimal", "")},
			}, {
				ID: "stations", Name: "Stations", Adopted: true, PrimaryKeys: []string{"station_id"}, DisplayKey: "station_name",
				Table: interfaces.DraftTable{ResourceID: "r_stations", Name: "stations", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{draftProp("station_id", "integer", ""),
					draftProp("station_name", "string", "")},
			}},
			RelationTypes: []*interfaces.DraftRelationType{{
				ID: "shipments_from", Name: "Origin station", Description: "Where the shipment leaves from",
				Source: "shipments", Target: "stations", Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping:  []interfaces.DraftFieldMapping{{SourceProperty: "origin_station", TargetProperty: "station_id"}},
				Evidence: interfaces.DraftJoinEvidence{Computed: true, Rows: 50, Hits: 50, HitRate: 1},
			}, {
				ID: "shipments_to", Name: "Destination station", Source: "shipments", Target: "stations",
				Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping:     []interfaces.DraftFieldMapping{{SourceProperty: "dest_station", TargetProperty: "station_id"}},
			}},
		},
	}
}

// visitDraft holds clinic visits and the patients they are of.
func visitDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{{
				ID: "visits", Name: "Visits", NeedsIdentity: true,
				Table: interfaces.DraftTable{ResourceID: "r_visits", Name: "visits", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{draftProp("visit_id", "string", ""),
					draftProp("patient_ref", "string", ""), draftProp("seen_at", "datetime", "")},
				Key: interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_NONE},
			}, {
				ID: "patients", Name: "Patients", PrimaryKeys: []string{"patient_id"},
				Table:      interfaces.DraftTable{ResourceID: "r_patients", Name: "patients", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{draftProp("patient_id", "string", "")},
			}},
			RelationTypes: []*interfaces.DraftRelationType{{
				ID: "visits_of", Name: "Patient of visit", Source: "visits", Target: "patients",
				Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping:     []interfaces.DraftFieldMapping{{SourceProperty: "patient_ref", TargetProperty: "patient_id"}},
			}},
		},
	}
}

// chatTurn is a turn of the chat Agent as bkn-agent streams it to Chat.
type chatTurn = func(context.Context, interfaces.AccountInfo, *interfaces.BknAgentChatRequest,
	func(interfaces.BknAgentChatEvent)) error

func chatEvent(name string, data string) interfaces.BknAgentChatEvent {
	return interfaces.BknAgentChatEvent{Name: name, Data: json.RawMessage(data)}
}

func tokenEvent(text string) interfaces.BknAgentChatEvent {
	data, _ := json.Marshal(map[string]string{"content": text})
	return chatEvent(interfaces.BKN_AGENT_CHAT_TOKEN, string(data))
}

// streamed streams meta and then the events given.
func streamed(events ...interfaces.BknAgentChatEvent) chatTurn {
	return func(_ context.Context, _ interfaces.AccountInfo, _ *interfaces.BknAgentChatRequest,
		on func(interfaces.BknAgentChatEvent)) error {
		on(chatEvent("meta", `{"thread_id":"th1"}`))
		for _, e := range events {
			on(e)
		}
		return nil
	}
}

// answered streams the text in pieces, then the result and done.
func answered(result string, pieces ...string) chatTurn {
	events := []interfaces.BknAgentChatEvent{}
	for _, piece := range pieces {
		events = append(events, tokenEvent(piece))
	}
	return streamed(append(events, chatEvent(interfaces.BKN_AGENT_CHAT_STRUCTURED, `{"content":`+result+`}`),
		chatEvent(interfaces.BKN_AGENT_CHAT_DONE, `{}`))...)
}

// revised answers 答复 with the revisions given as JSON.
func revised(revisions ...string) chatTurn {
	return answered(`{"revisions":[`+strings.Join(revisions, ",")+`]}`, "答", "复")
}

func Test_AskObjectType(t *testing.T) {
	Convey("Test asking AI about an object type\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		// The draft is only read: any write to it, a decision or an id registered fails the test,
		// as these mocks expect no such call and the id registry is left out.
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, ba: ba}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		readDraft := func(d *interfaces.Draft) {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
		}
		askAbout := func(item string, req interfaces.DraftAskRequest) (*interfaces.DraftAskAnswer, error) {
			return svc.AskObjectType(ctx, token, "p1", item, req, caller, nil)
		}
		ask := func(q string) (*interfaces.DraftAskAnswer, error) {
			return askAbout("employees", interfaces.DraftAskRequest{Question: q})
		}
		agentAnswers := func(turn chatTurn) {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(turn)
		}

		Convey("It asks as the caller about the object type as the editor has it, with the last turns\n", func() {
			readDraft(staffDraft())
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					So(req.AgentID, ShouldEqual, askAgentID)
					schema, _ := json.Marshal(req.ResponseFormat)
					So(string(schema), ShouldContainSubstring, `"enum":["name","comment","primary_keys","display_key","data_property"]`)
					var in askInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(in.Question, ShouldEqual, "工号能做标识吗？")
					So(in.Kind, ShouldEqual, askKindObjectType)
					So(in.RelationType, ShouldBeNil)
					// The form's name, identity and property comment; full_name left out of the form is gone.
					So(in.ObjectType.Name, ShouldEqual, "员工")
					So(in.ObjectType.Identity, ShouldResemble, []string{"emp_no"})
					So(in.ObjectType.NeedsIdentity, ShouldBeFalse)
					// The form's identity has not been counted.
					So(in.ObjectType.IdentityCount, ShouldResemble, interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_CHOSEN})
					So(len(in.ObjectType.Properties), ShouldEqual, 3)
					So(in.ObjectType.Properties[2].Description, ShouldEqual, "税前月薪")
					// Only the relation types it is an end of, with their counts, never rows.
					So(len(in.Relations), ShouldEqual, 1)
					So(*in.Relations[0].HitRate, ShouldEqual, 0.95)
					// The last turns that hold a question, each bounded.
					So(len(in.History), ShouldEqual, 3)
					So(in.History[0].Question, ShouldEqual, "q2")
					So([]rune(in.History[2].Answer), ShouldHaveLength, askAnswerMaxLength)
					return answered(`{"revisions":[]}`, "  可以，", "工号没有重复。 ")(ctx, account, req, on)
				})

			got, err := askAbout("employees", interfaces.DraftAskRequest{Question: "  工号能做标识吗？ ",
				Working: &interfaces.DraftObjectTypeSave{Name: "员工", PrimaryKeys: []string{"emp_no"},
					DataProperties: []interfaces.DraftDataPropertySave{
						{Name: "emp_no"}, {Name: "dept_code"}, {Name: "salary", Comment: "税前月薪"}, {Name: "added_in_form"}}},
				History: []interfaces.DraftAskTurn{{Question: "q1"}, {Question: "q2", Answer: "a2"}, {Question: " "},
					{Question: "q3"}, {Question: "q4", Answer: strings.Repeat("答", askAnswerMaxLength+1)}}})

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "可以，工号没有重复。")
			So(got.Revisions, ShouldBeEmpty)
			So(got.Dropped, ShouldBeEmpty)
			So(got.Suggestion, ShouldBeNil)
		})

		Convey("Revisions the save would take are kept in the save body's terms; the first an edit takes is the suggestion\n", func() {
			readDraft(staffDraft())
			agentAnswers(revised(
				`{"field":"primary_keys","properties":["emp_no"],"reason":" 工号唯一 "}`,
				`{"field":"name","value":" 员工 ","reason":"中文名"}`,
				`{"field":"display_key","value":"full_name","reason":"按姓名称呼"}`,
				`{"field":"data_property","property":{"name":"salary","display_name":"月薪"},"reason":"更清楚"}`,
			))

			got, err := ask("怎么改？")

			So(err, ShouldBeNil)
			So(got.Dropped, ShouldBeEmpty)
			So(got.Revisions, ShouldResemble, []interfaces.DraftAskRevision{
				{Field: interfaces.DRAFT_ASK_REVISION_PRIMARY_KEYS, Value: []string{"emp_no"}, Reason: "工号唯一"},
				{Field: interfaces.DRAFT_ASK_REVISION_NAME, Value: "员工", Reason: "中文名"},
				{Field: interfaces.DRAFT_ASK_REVISION_DISPLAY_KEY, Value: "full_name", Reason: "按姓名称呼"},
				// A comment left out keeps the property's own, so saving the revision clears nothing.
				{Field: interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY, Value: interfaces.DraftAskPropertyRevision{
					Name: "salary", DisplayName: "月薪", Comment: "Monthly, before tax"}, Reason: "更清楚"},
			})
			So(got.Suggestion, ShouldResemble, &interfaces.DraftAskSuggestion{
				Field: interfaces.DRAFT_ASK_FIELD_IDENTITY, Properties: []string{"emp_no"}, Reason: "工号唯一"})
		})

		Convey("A revision the save would refuse, or that changes nothing, is dropped with why; the answer stays\n", func() {
			readDraft(staffDraft())
			agentAnswers(revised(
				`{"field":"name","value":"Employees"}`,
				`{"field":"name","value":"员工"}`,
				`{"field":"primary_keys","properties":["salary"]}`,
				`{"field":"display_key","value":"nickname"}`,
				`{"field":"ends","source_object_type_id":"employees"}`,
				`{"field":"comment","value":"超出上限的第六条"}`,
			))

			got, err := ask("怎么改？")

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "答复")
			So(got.Revisions, ShouldResemble, []interfaces.DraftAskRevision{
				{Field: interfaces.DRAFT_ASK_REVISION_NAME, Value: "员工"}})
			// No more than askMaxRevisions are read: the sixth is neither kept nor dropped.
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{
				{Field: "name", Reason: askDroppedUnchanged},
				{Field: "primary_keys", Reason: askDroppedInvalidIdentity},
				{Field: "display_key", Reason: askDroppedUnknownProperty},
				{Field: "ends", Reason: askDroppedNotEditable},
			})
		})

		Convey("Each field is revised once and each property once; names and properties are bounded as saved\n", func() {
			readDraft(staffDraft())
			agentAnswers(revised(
				`{"field":"comment","value":"在职员工"}`,
				`{"field":"comment","value":"全部员工"}`,
				`{"field":"data_property","property":{"name":"full_name","display_name":"姓名"}}`,
				`{"field":"data_property","property":{"name":"full_name","display_name":"全名"}}`,
				`{"field":"data_property","property":{"name":"dept_code","display_name":"`+strings.Repeat("名", interfaces.DRAFT_NAME_MAX_LENGTH+1)+`"}}`,
			))

			got, err := ask("怎么改？")

			So(err, ShouldBeNil)
			So(len(got.Revisions), ShouldEqual, 2)
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{
				{Field: "comment", Reason: askDroppedRepeated},
				{Field: "data_property", Reason: askDroppedRepeated},
				{Field: "data_property", Reason: askDroppedTooLong},
			})
			// A description is the suggestion only within its bound.
			So(got.Suggestion, ShouldResemble, &interfaces.DraftAskSuggestion{
				Field: interfaces.DRAFT_ASK_FIELD_DESCRIPTION, Value: "在职员工"})
		})

		Convey("Unknown properties, blank names and a long name are dropped too\n", func() {
			readDraft(staffDraft())
			agentAnswers(revised(
				`{"field":"data_property","property":{"name":"bonus","display_name":"奖金"}}`,
				`{"field":"data_property"}`,
				`{"field":"name","value":"  "}`,
				`{"field":"name","value":"`+strings.Repeat("名", interfaces.DRAFT_NAME_MAX_LENGTH+1)+`"}`,
				`{"field":"primary_keys","properties":["emp_no","emp_no"]}`,
			))

			got, err := ask("怎么改？")

			So(err, ShouldBeNil)
			So(got.Revisions, ShouldBeEmpty)
			So(got.Suggestion, ShouldBeNil)
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{
				{Field: "data_property", Reason: askDroppedUnknownProperty},
				{Field: "data_property", Reason: askDroppedUnknownProperty},
				{Field: "name", Reason: askDroppedInvalidName},
				{Field: "name", Reason: askDroppedInvalidName},
				{Field: "primary_keys", Reason: askDroppedInvalidIdentity},
			})
		})

		Convey("What an object type with an identity already has is unchanged; a long comment is no suggestion\n", func() {
			readDraft(shipmentDraft())
			long := strings.Repeat("长", askValueMaxLength+1)
			agentAnswers(revised(
				`{"field":"primary_keys","properties":["waybill"]}`,
				`{"field":"display_key","value":"waybill"}`,
				`{"field":"data_property","property":{"name":"weight","display_name":"","comment":""}}`,
				`{"field":"comment","value":"  "}`,
				`{"field":"comment","value":"`+long+`"}`,
			))

			got, err := askAbout("shipments", interfaces.DraftAskRequest{Question: "怎么改？"})

			So(err, ShouldBeNil)
			So(got.Revisions, ShouldResemble, []interfaces.DraftAskRevision{
				{Field: interfaces.DRAFT_ASK_REVISION_COMMENT, Value: long}})
			So(got.Suggestion, ShouldBeNil)
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{
				{Field: "primary_keys", Reason: askDroppedUnchanged},
				{Field: "display_key", Reason: askDroppedUnchanged},
				{Field: "data_property", Reason: askDroppedUnchanged},
				{Field: "comment", Reason: askDroppedUnchanged},
			})
		})

		Convey("A form that keeps the identity and the join keeps their counts\n", func() {
			d := shipmentDraft()
			d.Items.ObjectTypes[0].Key = interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_PRIMARY_KEY, Computed: true, Rows: 50, Distinct: 50}
			readDraft(d)
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					var in askInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(in.ObjectType.IdentityCount.Computed, ShouldBeTrue)
					So(in.ObjectType.Name, ShouldEqual, "运单")
					return answered(`{"revisions":[]}`, "好")(ctx, account, req, on)
				})

			_, err := askAbout("shipments", interfaces.DraftAskRequest{Question: "问",
				Working: &interfaces.DraftObjectTypeSave{Name: "运单", PrimaryKeys: []string{"waybill"}}})

			So(err, ShouldBeNil)
		})

		Convey("Another domain: the identity of visits, which still need one confirmed\n", func() {
			// Drafting proposed it; it still needs a person's choice, so proposing it again is a change.
			d := visitDraft()
			d.Items.ObjectTypes[0].PrimaryKeys = []string{"visit_id"}
			readDraft(d)
			agentAnswers(revised(`{"field":"primary_keys","properties":["visit_id"],"reason":"每次就诊一个编号"}`))

			got, err := askAbout("visits", interfaces.DraftAskRequest{Question: "用什么标识一次就诊？"})

			So(err, ShouldBeNil)
			So(got.Revisions, ShouldResemble, []interfaces.DraftAskRevision{{
				Field: interfaces.DRAFT_ASK_REVISION_PRIMARY_KEYS, Value: []string{"visit_id"}, Reason: "每次就诊一个编号"}})
		})

		Convey("A streamed answer starts once bkn-agent answers and hands on each piece in order\n", func() {
			readDraft(staffDraft())
			agentAnswers(revised(`{"field":"name","value":"员工"}`))
			var seen []string
			stream := &interfaces.DraftAskStream{Start: func() { seen = append(seen, "start") },
				Token: func(text string) { seen = append(seen, text) }}

			got, err := svc.AskObjectType(ctx, token, "p1", "employees", interfaces.DraftAskRequest{Question: "问"}, caller, stream)

			So(err, ShouldBeNil)
			So(seen, ShouldResemble, []string{"start", "答", "复"})
			So(got.Answer, ShouldEqual, "答复")
			So(len(got.Revisions), ShouldEqual, 1)
		})

		Convey("A long answer is cut to its bound in the result\n", func() {
			readDraft(staffDraft())
			agentAnswers(answered(`{"revisions":[]}`, strings.Repeat("长", askAnswerMaxLength), "多"))

			got, err := ask("问")

			So(err, ShouldBeNil)
			So([]rune(got.Answer), ShouldHaveLength, askAnswerMaxLength)
		})

		Convey("An answer that does not end in time is a timeout, the text so far handed on\n", func() {
			svc.askWait = 20 * time.Millisecond
			readDraft(staffDraft())
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ interfaces.AccountInfo, _ *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					on(chatEvent("meta", `{}`))
					on(tokenEvent("一半"))
					<-ctx.Done()
					return ctx.Err()
				})
			var text []string
			stream := &interfaces.DraftAskStream{Start: func() {}, Token: func(t string) { text = append(text, t) }}

			_, err := svc.AskObjectType(ctx, token, "p1", "employees", interfaces.DraftAskRequest{Question: "问"}, caller, stream)

			shouldBeHTTPError(err, http.StatusGatewayTimeout, perrors.BknProject_Draft_AskTimeout)
			So(text, ShouldResemble, []string{"一半"})
		})

		Convey("bkn-agent running out of time is a timeout too\n", func() {
			readDraft(staffDraft())
			agentAnswers(streamed(tokenEvent("一半"),
				chatEvent(interfaces.BKN_AGENT_CHAT_ERROR, `{"code":"BknAgent.Chat.Timeout","detail":"40"}`)))

			_, err := ask("问")

			shouldBeHTTPError(err, http.StatusGatewayTimeout, perrors.BknProject_Draft_AskTimeout)
		})

		Convey("A request cut while answering is unavailable, not a timeout\n", func() {
			readDraft(staffDraft())
			cut, cancel := context.WithCancel(ctx)
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ interfaces.AccountInfo, _ *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					cancel()
					return ctx.Err()
				})

			_, err := svc.AskObjectType(cut, token, "p1", "employees", interfaces.DraftAskRequest{Question: "问"}, caller, nil)

			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Draft_AskUnavailable)
		})

		Convey("An Agent that is refused, fails, is cut or answers outside its schema is unavailable\n", func() {
			started := false
			stream := &interfaces.DraftAskStream{Start: func() { started = true }, Token: func(string) {}}
			for _, turn := range []chatTurn{
				func(context.Context, interfaces.AccountInfo, *interfaces.BknAgentChatRequest, func(interfaces.BknAgentChatEvent)) error {
					return &interfaces.BknAgentError{HTTPCode: http.StatusBadRequest, Code: "BknAgent.Chat.ModeMismatch"}
				},
				// Failing after its result, as when recording the turn fails.
				streamed(tokenEvent("答"), chatEvent(interfaces.BKN_AGENT_CHAT_STRUCTURED, `{"content":{"revisions":[]}}`),
					chatEvent(interfaces.BKN_AGENT_CHAT_ERROR, `{"code":"BknAgent.Chat.Failed"}`)),
				func(_ context.Context, _ interfaces.AccountInfo, _ *interfaces.BknAgentChatRequest, on func(interfaces.BknAgentChatEvent)) error {
					on(tokenEvent("答"))
					return interfaces.ErrBknAgentAnswerCut
				},
				streamed(tokenEvent("答"), chatEvent(interfaces.BKN_AGENT_CHAT_DONE, `{}`)),
				streamed(tokenEvent("答"), chatEvent(interfaces.BKN_AGENT_CHAT_STRUCTURED, `{"content":"not an object"}`),
					chatEvent(interfaces.BKN_AGENT_CHAT_DONE, `{}`)),
				answered(`{"revisions":[]}`, " "),
			} {
				readDraft(staffDraft())
				agentAnswers(turn)

				_, err := svc.AskObjectType(ctx, token, "p1", "employees", interfaces.DraftAskRequest{Question: "问"}, caller, stream)

				shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Draft_AskUnavailable)
			}
			So(started, ShouldBeTrue)
		})

		Convey("A refusal before answering does not start the stream\n", func() {
			readDraft(staffDraft())
			agentAnswers(func(context.Context, interfaces.AccountInfo, *interfaces.BknAgentChatRequest, func(interfaces.BknAgentChatEvent)) error {
				return &interfaces.BknAgentError{HTTPCode: http.StatusNotFound, Code: "BknAgent.NotFound"}
			})
			started := false

			_, err := svc.AskObjectType(ctx, token, "p1", "employees", interfaces.DraftAskRequest{Question: "问"}, caller,
				&interfaces.DraftAskStream{Start: func() { started = true }, Token: func(string) {}})

			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Draft_AskUnavailable)
			So(started, ShouldBeFalse)
		})

		Convey("A blank or long question is refused before anything is read\n", func() {
			for _, q := range []string{"  ", strings.Repeat("问", interfaces.DRAFT_ASK_QUESTION_MAX_LENGTH+1)} {
				_, err := ask(q)

				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidQuestion)
			}
		})

		Convey("Only a ready draft's object types are asked about, and the Agent is not run otherwise\n", func() {
			d := staffDraft()
			d.Status = interfaces.DRAFT_STATUS_DRAFTING
			readDraft(d)
			_, err := ask("问")
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_NotReady)

			readDraft(staffDraft())
			_, err = askAbout("ghost", interfaces.DraftAskRequest{Question: "问"})
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
		})
	})
}

func Test_AskRelationType(t *testing.T) {
	Convey("Test asking AI about a relation type\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		// Ends are checked as a direct mapping on a copy: nothing is read from vega and nothing written.
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, ba: ba}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		readDraft := func(d *interfaces.Draft) {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
		}
		askAbout := func(item string, req interfaces.DraftRelationAskRequest) (*interfaces.DraftAskAnswer, error) {
			return svc.AskRelationType(ctx, token, "p1", item, req, caller, nil)
		}
		agentAnswers := func(turn chatTurn) {
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(turn)
		}
		ends := func(source string, target string, mapping ...string) interfaces.DraftAskEndsRevision {
			e := interfaces.DraftAskEndsRevision{SourceObjectTypeID: source, TargetObjectTypeID: target,
				Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: []interfaces.DraftRelationMapping{}}
			for i := 0; i < len(mapping); i += 2 {
				e.MappingRules = append(e.MappingRules, interfaces.DraftRelationMapping{
					SourceProperty: interfaces.DraftMappedField{Name: mapping[i]},
					TargetProperty: interfaces.DraftMappedField{Name: mapping[i+1]}})
			}
			return e
		}

		Convey("It asks about the relation type and both its ends\n", func() {
			readDraft(shipmentDraft())
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					schema, _ := json.Marshal(req.ResponseFormat)
					So(string(schema), ShouldContainSubstring, `"enum":["name","comment","ends"]`)
					var in askInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(in.Kind, ShouldEqual, askKindRelationType)
					So(in.ObjectType, ShouldBeNil)
					// The form's mapping, defined as the save would.
					So(in.RelationType.Name, ShouldEqual, "途经站")
					So(in.RelationType.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{
						{SourceProperty: "via_station", TargetProperty: "station_id"}})
					// The form's join has not been counted.
					So(in.RelationType.HitRate, ShouldBeNil)
					So(len(in.Ends), ShouldEqual, 2)
					So(in.Ends[0].ID, ShouldEqual, "shipments")
					So(in.Ends[1].Identity, ShouldResemble, []string{"station_id"})
					return answered(`{"revisions":[]}`, "它按站点编号关联。")(ctx, account, req, on)
				})
			rules, _ := json.Marshal(ends("shipments", "stations", "via_station", "station_id").MappingRules)

			got, err := askAbout("shipments_from", interfaces.DraftRelationAskRequest{Question: "这条关系对吗？",
				Working: &interfaces.DraftRelationTypeSave{Name: "途经站", SourceObjectTypeID: "shipments",
					TargetObjectTypeID: "stations", Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: rules}})

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "它按站点编号关联。")
		})

		Convey("A form that keeps the join keeps its count\n", func() {
			readDraft(shipmentDraft())
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					var in askInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(*in.RelationType.HitRate, ShouldEqual, 1)
					So(in.RelationType.Name, ShouldEqual, "起运站")
					return answered(`{"revisions":[]}`, "好")(ctx, account, req, on)
				})
			rules, _ := json.Marshal(ends("shipments", "stations", "origin_station", "station_id").MappingRules)

			_, err := askAbout("shipments_from", interfaces.DraftRelationAskRequest{Question: "问",
				Working: &interfaces.DraftRelationTypeSave{Name: "起运站", SourceObjectTypeID: "shipments",
					TargetObjectTypeID: "stations", Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: rules}})

			So(err, ShouldBeNil)
		})

		Convey("A form whose mapping the save would refuse leaves the draft's join to ask about\n", func() {
			readDraft(shipmentDraft())
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, account interfaces.AccountInfo, req *interfaces.BknAgentChatRequest,
					on func(interfaces.BknAgentChatEvent)) error {
					var in askInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(in.RelationType.Mapping[0].SourceProperty, ShouldEqual, "origin_station")
					So(*in.RelationType.HitRate, ShouldEqual, 1)
					return answered(`{"revisions":[]}`, "好")(ctx, account, req, on)
				})

			_, err := askAbout("shipments_from", interfaces.DraftRelationAskRequest{Question: "问",
				Working: &interfaces.DraftRelationTypeSave{Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: []byte(`[]`)}})

			So(err, ShouldBeNil)
		})

		Convey("Ends the save would define are kept as the save body has them; no suggestion is given\n", func() {
			readDraft(shipmentDraft())
			agentAnswers(revised(
				`{"field":"ends","mapping":[{"source_property":"via_station","target_property":"station_id"}],"reason":"按途经站关联"}`,
				`{"field":"comment","value":"货物经过的站点"}`,
			))

			got, err := askAbout("shipments_from", interfaces.DraftRelationAskRequest{Question: "怎么改？"})

			So(err, ShouldBeNil)
			So(got.Dropped, ShouldBeEmpty)
			So(got.Revisions, ShouldResemble, []interfaces.DraftAskRevision{
				{Field: interfaces.DRAFT_ASK_REVISION_ENDS, Value: ends("shipments", "stations", "via_station", "station_id"),
					Reason: "按途经站关联"},
				{Field: interfaces.DRAFT_ASK_REVISION_COMMENT, Value: "货物经过的站点"},
			})
			So(got.Suggestion, ShouldBeNil)
		})

		Convey("Ends the save would refuse are dropped with the save's reason\n", func() {
			readDraft(shipmentDraft())
			agentAnswers(revised(
				`{"field":"ends","mapping":[{"source_property":"origin_station","target_property":"station_id"}]}`,
				`{"field":"ends","target_object_type_id":"depots","mapping":[{"source_property":"origin_station","target_property":"depot_id"}]}`,
				`{"field":"ends","mapping":[{"source_property":"origin_station","target_property":"station_name"}]}`,
				`{"field":"ends","mapping":[{"source_property":"dest_station","target_property":"station_id"}]}`,
				`{"field":"ends","mapping":[{"source_property":"route","target_property":"station_id"}]}`,
			))

			got, err := askAbout("shipments_from", interfaces.DraftRelationAskRequest{Question: "怎么改？"})

			So(err, ShouldBeNil)
			So(got.Revisions, ShouldBeEmpty)
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{
				{Field: "ends", Reason: askDroppedUnchanged},
				{Field: "ends", Reason: relationUnknownEnd},
				{Field: "ends", Reason: relationTypeMismatch},
				// The destination relation already joins by it.
				{Field: "ends", Reason: relationDuplicate},
				{Field: "ends", Reason: relationUnknownProperty},
			})
		})

		Convey("No mapping, and fields of an object type, are dropped\n", func() {
			readDraft(visitDraft())
			agentAnswers(revised(`{"field":"ends"}`, `{"field":"primary_keys","properties":["visit_id"]}`,
				`{"field":"name","value":"就诊患者"}`))

			got, err := askAbout("visits_of", interfaces.DraftRelationAskRequest{Question: "怎么改？"})

			So(err, ShouldBeNil)
			So(got.Revisions, ShouldResemble, []interfaces.DraftAskRevision{
				{Field: interfaces.DRAFT_ASK_REVISION_NAME, Value: "就诊患者"}})
			So(got.Dropped, ShouldResemble, []interfaces.DraftAskDropped{
				{Field: "ends", Reason: relationNoMapping},
				{Field: "primary_keys", Reason: askDroppedNotEditable},
			})
		})

		Convey("Only a ready draft's relation types are asked about\n", func() {
			readDraft(visitDraft())
			_, err := askAbout("ghost", interfaces.DraftRelationAskRequest{Question: "问"})
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)

			_, err = askAbout("visits_of", interfaces.DraftRelationAskRequest{Question: " "})
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidQuestion)
		})
	})
}

// Test_AskRevisionsSave checks that what Ask AI keeps, merged into the form, is what the save takes.
func Test_AskRevisionsSave(t *testing.T) {
	Convey("Test saving the revisions Ask AI keeps\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, ba: ba, va: va, ira: &fakeIDRegistry{}}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		// Counting after the save may fail: the save keeps what it was given.
		va.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("busy")).AnyTimes()
		va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return([]*interfaces.VegaTable{employeesTable()}, nil).AnyTimes()
		da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil).AnyTimes()

		Convey("An object type's revisions merged into its form are saved\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
				return staffDraft(), true, nil
			}).Times(2)
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(revised(
				`{"field":"primary_keys","properties":["emp_no"]}`, `{"field":"name","value":"员工"}`,
				`{"field":"comment","value":"在职员工"}`, `{"field":"display_key","value":"full_name"}`,
				`{"field":"data_property","property":{"name":"salary","display_name":"月薪"}}`))
			got, err := svc.AskObjectType(ctx, token, "p1", "employees", interfaces.DraftAskRequest{Question: "怎么改？"}, caller, nil)
			So(err, ShouldBeNil)
			So(len(got.Revisions), ShouldEqual, 5)

			save := saveOf(staffDraft())
			for _, r := range got.Revisions {
				switch v := r.Value.(type) {
				case []string:
					save.PrimaryKeys = v
				case interfaces.DraftAskPropertyRevision:
					for i := range save.DataProperties {
						if save.DataProperties[i].Name == v.Name {
							save.DataProperties[i].DisplayName, save.DataProperties[i].Comment = v.DisplayName, v.Comment
						}
					}
				case string:
					switch r.Field {
					case interfaces.DRAFT_ASK_REVISION_NAME:
						save.Name = v
					case interfaces.DRAFT_ASK_REVISION_COMMENT:
						save.Comment = v
					default:
						save.DisplayKey = v
					}
				}
			}
			saved, err := svc.SaveObjectType(ctx, token, "p1", "employees", save, caller)

			So(err, ShouldBeNil)
			ot := saved.Items.ObjectTypes[0]
			So(ot.Name, ShouldEqual, "员工")
			So(ot.PrimaryKeys, ShouldResemble, []string{"emp_no"})
			So(ot.DisplayKey, ShouldEqual, "full_name")
			So(findProperty(ot, "salary").DisplayName, ShouldEqual, "月薪")
			So(findProperty(ot, "salary").Description, ShouldEqual, "Monthly, before tax")
		})

		Convey("A relation type's ends are saved\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
				return shipmentDraft(), true, nil
			}).Times(2)
			ba.EXPECT().Chat(gomock.Any(), caller, gomock.Any(), gomock.Any()).DoAndReturn(revised(
				`{"field":"ends","mapping":[{"source_property":"via_station","target_property":"station_id"}]}`))
			got, err := svc.AskRelationType(ctx, token, "p1", "shipments_from", interfaces.DraftRelationAskRequest{Question: "怎么改？"}, caller, nil)
			So(err, ShouldBeNil)
			So(len(got.Revisions), ShouldEqual, 1)

			e := got.Revisions[0].Value.(interfaces.DraftAskEndsRevision)
			rules, _ := json.Marshal(e.MappingRules)
			saved, err := svc.SaveRelationType(ctx, token, "p1", "shipments_from", interfaces.DraftRelationTypeSave{
				UpdateTime: readTime, Name: "Origin station", SourceObjectTypeID: e.SourceObjectTypeID,
				TargetObjectTypeID: e.TargetObjectTypeID, Type: e.Type, MappingRules: rules}, caller)

			So(err, ShouldBeNil)
			So(findRelationType(&saved.Items, "shipments_from").Mapping, ShouldResemble, []interfaces.DraftFieldMapping{
				{SourceProperty: "via_station", TargetProperty: "station_id"}})
		})
	})
}
