// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_AskReply(t *testing.T) {
	Convey("Test cutting the JSON an Agent writes after its answer\n", t, func() {
		// reply streams the tokens and returns what was forwarded and the answer.
		reply := func(tokens ...string) ([]string, string) {
			forwarded := []string{}
			r := newAskReply(&interfaces.DraftAskStream{Token: func(text string) { forwarded = append(forwarded, text) }})
			for _, token := range tokens {
				r.add(token)
			}
			r.finish()
			return forwarded, r.answer()
		}

		Convey("Text with no marker is forwarded whole, a brace or backtick held only until it is no marker\n", func() {
			forwarded, answer := reply(" 集合 {", "a} 用 `", "x` 表示。``")

			So(strings.Join(forwarded, ""), ShouldEqual, " 集合 {a} 用 `x` 表示。``")
			So(forwarded[0], ShouldEqual, " 集合 ")
			So(answer, ShouldEqual, "集合 {a} 用 `x` 表示。``")
		})

		Convey("A code fence stops forwarding, also when split across tokens\n", func() {
			forwarded, answer := reply("它是归属关系。", " `", "`", "`json\n{\"revisions\":[]}\n```")

			So(strings.Join(forwarded, ""), ShouldEqual, "它是归属关系。 ")
			So(answer, ShouldEqual, "它是归属关系。")
		})

		Convey("An object of revisions stops forwarding, also when split across tokens\n", func() {
			forwarded, answer := reply("Rename it. ", "{\"rev", "isions\":[{\"field\":\"name\"}]}")

			So(strings.Join(forwarded, ""), ShouldEqual, "Rename it. ")
			So(answer, ShouldEqual, "Rename it.")
		})

		Convey("A reply that is JSON from its start is never forwarded; its answer is taken out\n", func() {
			forwarded, answer := reply("  {\"ans", "wer\":\" 它是归属关系。 \",\"revisions\":[]}")

			So(forwarded, ShouldBeEmpty)
			So(answer, ShouldEqual, "它是归属关系。")

			forwarded, answer = reply("```json\n{\"answer\":\"It belongs.\"}\n```")
			So(forwarded, ShouldBeEmpty)
			So(answer, ShouldEqual, "It belongs.")
		})

		Convey("A reply that is only JSON without an answer has none\n", func() {
			for _, tokens := range [][]string{
				{"{\"answer\":\"未写完"}, {"{\"answer\":\"\"}"}, {"{\"note\":\"x\"}"}, {"```json\n{\"revisions\":[]}\n```"}, {"  "},
			} {
				forwarded, answer := reply(tokens...)

				So(forwarded, ShouldBeEmpty)
				So(answer, ShouldEqual, "")
			}
		})

		Convey("Nothing is forwarded without a stream\n", func() {
			r := newAskReply(nil)
			r.add("好。")
			r.finish()

			So(r.answer(), ShouldEqual, "好。")
		})
	})
}
