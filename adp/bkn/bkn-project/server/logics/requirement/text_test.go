// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Tested on an energy ontology: devices, and their readings taken on a response day.

func readingsOntology() *interfaces.Draft {
	return &interfaces.Draft{Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{ID: "devices", Name: "Devices", Adopted: true, Properties: []*interfaces.DraftProperty{{Name: "device_id", DisplayName: "Device id"}}},
			{ID: "readings", Name: "Readings", Adopted: true, Properties: []*interfaces.DraftProperty{
				{Name: "response_date", DisplayName: "响应日"}, {Name: "read_at", DisplayName: "read_at"}}},
		},
	}}
}

func readingsSpec(term string) *interfaces.RequirementSpec {
	return &interfaces.RequirementSpec{Concepts: []*interfaces.RequirementConcept{
		{ID: "c1", Term: term, MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "devices"}},
		{ID: "c2", Term: "Reading", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "readings"}},
	}}
}

func slotQuestion(text string, inputs ...interfaces.RequirementInput) *interfaces.RequirementQuestion {
	return &interfaces.RequirementQuestion{Text: text, Template: text, Inputs: &inputs}
}

var (
	deviceInput = interfaces.RequirementInput{Name: "device", ConceptID: "c1", Property: "device_id", Role: interfaces.REQUIREMENT_INPUT_RECORD}
	dayInput    = interfaces.RequirementInput{Name: "response_date", ConceptID: "c2", Property: "response_date", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}
	readAtInput = interfaces.RequirementInput{Name: "read_at", ConceptID: "c2", Property: "read_at", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}
)

func Test_PlainText(t *testing.T) {
	Convey("Test saying a question's slots in business words\n", t, func() {
		draft := readingsOntology()
		zh, en := readingsSpec("设备"), readingsSpec("device")

		Convey("A record is a given one of its concept, said once where the text names the concept already\n", func() {
			So(plainText(slotQuestion("设备 {device} 在 响应日 {response_date} 的预测负荷是多少？", deviceInput, dayInput), zh, draft,
				interfaces.NAMING_LANGUAGE_ZH_CN), ShouldEqual, "指定的设备在指定的响应日的预测负荷是多少？")
			// Run into the word before it, a label is said again rather than cut out of that word.
			So(plainText(slotQuestion("设备 {device} 在响应日 {response_date} 的预测负荷是多少？", deviceInput, dayInput), zh, draft,
				interfaces.NAMING_LANGUAGE_ZH_CN), ShouldEqual, "指定的设备在响应日指定的响应日的预测负荷是多少？")
			So(plainText(slotQuestion("{device} 的读数有哪些？", deviceInput), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "指定的设备的读数有哪些？")
			So(plainText(slotQuestion("What did device {device} read on {response_date}?", deviceInput, dayInput), en, draft,
				interfaces.NAMING_LANGUAGE_EN_US), ShouldEqual, "What did a given device read on a given 响应日?")
			So(plainText(slotQuestion("{device} read what, on {read_at}?", deviceInput, readAtInput), en, draft,
				interfaces.NAMING_LANGUAGE_EN_US), ShouldEqual, "A given device read what, on a given date?")
		})

		Convey("A moment no display name says is a given date; a slot no input names reads by its name\n", func() {
			So(plainText(slotQuestion("从 {read_at} 起的读数有哪些？", readAtInput), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "从指定日期起的读数有哪些？")
			So(plainText(slotQuestion("以 {as_of} 为基准的近 30 天内结算多少？"), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "以指定日期为基准的近 30 天内结算多少？")
			So(plainText(slotQuestion("Which orders ship after { ship_date }?"), en, draft, interfaces.NAMING_LANGUAGE_EN_US),
				ShouldEqual, "Which orders ship after a given date?")
			So(plainText(slotQuestion("哪些记录属于 {region}？"), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "哪些记录属于指定的值？")
		})

		Convey("A label is said once only when the text names it as a word of its own\n", func() {
			So(plainText(slotQuestion("储能设备 {device} 的读数有哪些？", deviceInput), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "储能设备指定的设备的读数有哪些？")
			So(plainText(slotQuestion("What did the subdevice {device} read?", deviceInput), en, draft, interfaces.NAMING_LANGUAGE_EN_US),
				ShouldEqual, "What did the subdevice a given device read?")
			So(plainText(slotQuestion("请问，设备 {device} 在线吗？", deviceInput), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "请问，指定的设备在线吗？")
		})

		Convey("What a slot is said in is not read as a slot again, a label with braces too\n", func() {
			braced := readingsSpec("{device}")

			So(plainText(slotQuestion("哪些 {device} 在线，{device} 读数多少？", deviceInput), braced, draft, interfaces.NAMING_LANGUAGE_ZH_CN),
				ShouldEqual, "哪些指定的{device}在线，指定的{device}读数多少？")
		})

		Convey("Text without slots stays as written, braces that are no slot too\n", func() {
			for _, text := range []string{"哪些设备在线？", "Which {1} rows?", "Which set {a, b} holds it?", " Which rows ?"} {
				So(plainText(slotQuestion(text), zh, draft, interfaces.NAMING_LANGUAGE_ZH_CN), ShouldEqual, text)
			}
		})
	})
}
