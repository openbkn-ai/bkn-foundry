//go:build !skiptest
// +build !skiptest

package utils

import (
	"mime"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestAttachmentContentDisposition(t *testing.T) {
	Convey("AttachmentContentDisposition", t, func() {
		cases := []struct {
			name     string
			filename string
			fallback string
			want     string
		}{
			{
				name:     "ascii name is emitted once without filename*",
				filename: "demo-skill.zip",
				fallback: "skill.zip",
				want:     `attachment; filename="demo-skill.zip"`,
			},
			{
				name:     "chinese name gets ascii fallback plus RFC 5987 parameter",
				filename: "金额核对函数.zip",
				fallback: "skill.zip",
				want:     `attachment; filename="skill.zip"; filename*=UTF-8''%E9%87%91%E9%A2%9D%E6%A0%B8%E5%AF%B9%E5%87%BD%E6%95%B0.zip`,
			},
			{
				name:     "mixed name keeps the ascii part as fallback",
				filename: "金额核对 check (v2).zip",
				fallback: "skill.zip",
				want:     `attachment; filename="check (v2).zip"; filename*=UTF-8''%E9%87%91%E9%A2%9D%E6%A0%B8%E5%AF%B9%20check%20%28v2%29.zip`,
			},
			{
				name:     "spaces and parentheses survive in ascii names",
				filename: "my skill (final).zip",
				fallback: "skill.zip",
				want:     `attachment; filename="my skill (final).zip"`,
			},
			{
				name:     "percent and quotes never break the quoted-string",
				filename: `100% "done".zip`,
				fallback: "skill.zip",
				want:     `attachment; filename="100% _done_.zip"`,
			},
			{
				name:     "emoji is percent-encoded",
				filename: "rocket 🚀.zip",
				fallback: "skill.zip",
				want:     `attachment; filename="rocket.zip"; filename*=UTF-8''rocket%20%F0%9F%9A%80.zip`,
			},
			{
				name:     "CRLF injection is stripped",
				filename: "evil.zip\r\nX-Injected: 1",
				fallback: "skill.zip",
				want:     `attachment; filename="evil.zipX-Injected_ 1"`,
			},
			{
				name:     "path separators are neutralised",
				filename: `../../etc/passwd\..\win.zip`,
				fallback: "skill.zip",
				want:     `attachment; filename="etc_passwd_.._win.zip"`,
			},
			{
				name:     "empty name falls back to the default stem",
				filename: "",
				fallback: "skill.zip",
				want:     `attachment; filename="download"`,
			},
			{
				name:     "extension follows the unicode name, not the fallback",
				filename: "报告.tar",
				fallback: "skill.zip",
				want:     `attachment; filename="skill.tar"; filename*=UTF-8''%E6%8A%A5%E5%91%8A.tar`,
			},
		}
		for _, tc := range cases {
			Convey(tc.name, func() {
				got := AttachmentContentDisposition(tc.filename, tc.fallback)
				So(got, ShouldEqual, tc.want)
				// Every emitted header must be parseable by the standard library.
				_, params, err := mime.ParseMediaType(got)
				So(err, ShouldBeNil)
				So(params["filename"], ShouldNotBeEmpty)
			})
		}

		Convey("standard library decodes filename* back to the unicode name", func() {
			got := AttachmentContentDisposition("金额核对函数.zip", "skill.zip")
			_, params, err := mime.ParseMediaType(got)
			So(err, ShouldBeNil)
			So(params["filename"], ShouldEqual, "金额核对函数.zip")
		})
	})
}

func TestSanitizeAttachmentFilename(t *testing.T) {
	Convey("SanitizeAttachmentFilename", t, func() {
		So(SanitizeAttachmentFilename("金额核对函数.zip"), ShouldEqual, "金额核对函数.zip")
		So(SanitizeAttachmentFilename("  a/b\\c:d*e?f\"g<h>i|j.zip  "), ShouldEqual, "a_b_c_d_e_f_g_h_i_j.zip")
		So(SanitizeAttachmentFilename("a\x00b\x1fc\x7fd.zip"), ShouldEqual, "abcd.zip")
		So(SanitizeAttachmentFilename("..hidden"), ShouldEqual, "hidden")
		So(SanitizeAttachmentFilename("///"), ShouldEqual, "download")
		So(SanitizeAttachmentFilename(""), ShouldEqual, "download")
	})
}

func TestParseContentDispositionFilename(t *testing.T) {
	Convey("ParseContentDispositionFilename", t, func() {
		Convey("prefers filename* over filename", func() {
			got := ParseContentDispositionFilename(`attachment; filename="skill.zip"; filename*=UTF-8''%E9%87%91%E9%A2%9D.zip`)
			So(got, ShouldEqual, "金额.zip")
		})
		Convey("keeps ascii filename when filename* is absent", func() {
			So(ParseContentDispositionFilename(`attachment; filename="demo-skill.zip"`), ShouldEqual, "demo-skill.zip")
			So(ParseContentDispositionFilename(`attachment; filename=demo-skill.zip`), ShouldEqual, "demo-skill.zip")
		})
		Convey("accepts legacy raw utf-8 inside quotes", func() {
			So(ParseContentDispositionFilename(`attachment; filename="金额.zip"`), ShouldEqual, "金额.zip")
		})
		Convey("strips path components from untrusted headers", func() {
			So(ParseContentDispositionFilename(`attachment; filename="../../x.zip"`), ShouldEqual, "x.zip")
		})
		Convey("returns empty for missing or unusable values", func() {
			So(ParseContentDispositionFilename(""), ShouldEqual, "")
			So(ParseContentDispositionFilename("attachment"), ShouldEqual, "")
			So(ParseContentDispositionFilename(`attachment; filename=""`), ShouldEqual, "")
			So(ParseContentDispositionFilename(`attachment; filename="///"`), ShouldEqual, "")
		})
	})
}
