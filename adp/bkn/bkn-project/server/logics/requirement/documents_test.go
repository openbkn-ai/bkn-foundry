// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const token = "Bearer t"

var caller = interfaces.AccountInfo{ID: "u1", Type: "user"}

func shouldBeHTTPError(err error, status int, code string) {
	var httpErr *httperrors.HTTPError
	So(errors.As(err, &httpErr), ShouldBeTrue)
	So(httpErr.HTTPCode, ShouldEqual, status)
	So(httpErr.ErrorCode, ShouldEqual, code)
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func Test_Segment(t *testing.T) {
	Convey("Test segment\n", t, func() {
		Convey("Markdown splits at headings and blank lines, and each block knows its heading\n", func() {
			content := "# Purpose\nWhy we build it.\nSecond line.\n\n## Scope\n\nIn scope.\n\n\nOut of scope."
			blocks := segment(content, interfaces.REQUIREMENT_FORMAT_MARKDOWN)
			So(blocks, ShouldResemble, []interfaces.RequirementBlock{
				{ID: "b1", Heading: "Purpose", Start: 0, End: 9},
				{ID: "b2", Heading: "Purpose", Start: 10, End: 39},
				{ID: "b3", Heading: "Scope", Start: 41, End: 49},
				{ID: "b4", Heading: "Scope", Start: 51, End: 60},
				{ID: "b5", Heading: "Scope", Start: 63, End: 76},
			})
			runes := []rune(content)
			So(string(runes[blocks[1].Start:blocks[1].End]), ShouldEqual, "Why we build it.\nSecond line.")
		})

		Convey("Offsets count characters, not bytes\n", func() {
			content := "# 目的\n回答问题。\n\n范围"
			blocks := segment(content, interfaces.REQUIREMENT_FORMAT_MARKDOWN)
			runes := []rune(content)
			So(blocks, ShouldHaveLength, 3)
			So(string(runes[blocks[1].Start:blocks[1].End]), ShouldEqual, "回答问题。")
			So(blocks[2].Heading, ShouldEqual, "目的")
			So(string(runes[blocks[2].Start:blocks[2].End]), ShouldEqual, "范围")
		})

		Convey("Plain text splits at blank lines only; a # line is text\n", func() {
			blocks := segment("# not a heading\nstill one\n\nnext", interfaces.REQUIREMENT_FORMAT_TEXT)
			So(blocks, ShouldResemble, []interfaces.RequirementBlock{
				{ID: "b1", Start: 0, End: 25},
				{ID: "b2", Start: 27, End: 31},
			})
		})

		Convey("A Markdown heading needs a space after its marks\n", func() {
			blocks := segment("#tag line\n####### seven", interfaces.REQUIREMENT_FORMAT_MARKDOWN)
			So(blocks, ShouldHaveLength, 1)
			So(blocks[0].Heading, ShouldEqual, "")
		})

		Convey("Whitespace-only lines separate blocks, and an empty text has none\n", func() {
			So(segment("a\n  \t\nb", interfaces.REQUIREMENT_FORMAT_TEXT), ShouldHaveLength, 2)
			So(segment("", interfaces.REQUIREMENT_FORMAT_TEXT), ShouldBeEmpty)
		})
	})
}

func Test_ReadUpload(t *testing.T) {
	Convey("Test readUpload\n", t, func() {
		Convey("The format comes from the extension, in any case\n", func() {
			for name, format := range map[string]string{
				"a.md": interfaces.REQUIREMENT_FORMAT_MARKDOWN, "B.MARKDOWN": interfaces.REQUIREMENT_FORMAT_MARKDOWN,
				"c.Txt": interfaces.REQUIREMENT_FORMAT_TEXT,
			} {
				got, code := readUpload(interfaces.RequirementUpload{Name: name, Content: []byte("x")})
				So(code, ShouldEqual, "")
				So(got.format, ShouldEqual, format)
			}
			for _, name := range []string{"a.pdf", "a.docx", "md", "a.md.exe", ""} {
				_, code := readUpload(interfaces.RequirementUpload{Name: name, Content: []byte("x")})
				So(code, ShouldEqual, perrors.BknProject_Requirement_UnsupportedFormat)
			}
		})

		Convey("Only UTF-8 text without NUL bytes is accepted\n", func() {
			for _, content := range [][]byte{{0xd6, 0xd0, 0xce, 0xc4}, []byte("a\x00b"), {}} {
				_, code := readUpload(interfaces.RequirementUpload{Name: "a.txt", Content: content})
				So(code, ShouldEqual, perrors.BknProject_Requirement_NotText)
			}
		})

		Convey("Line ends and a byte order mark are normalized before hashing\n", func() {
			a, _ := readUpload(interfaces.RequirementUpload{Name: "a.md", Content: []byte("\ufeffx\r\ny\rz")})
			b, _ := readUpload(interfaces.RequirementUpload{Name: "b.md", Content: []byte("x\ny\nz")})
			So(a.content, ShouldEqual, "x\ny\nz")
			So(a.sha256, ShouldEqual, b.sha256)
			So(a.sha256, ShouldEqual, hashOf("x\ny\nz"))
		})

		Convey("A file over the size limit is refused\n", func() {
			_, code := readUpload(interfaces.RequirementUpload{Name: "a.txt", Content: []byte(strings.Repeat("a", MaxDocumentBytes+1))})
			So(code, ShouldEqual, perrors.BknProject_Requirement_DocumentTooLarge)
			_, code = readUpload(interfaces.RequirementUpload{Name: "a.txt", Content: []byte(strings.Repeat("a", MaxDocumentBytes))})
			So(code, ShouldEqual, "")
		})

		Convey("A name that is not UTF-8 is refused, as this file only\n", func() {
			_, code := readUpload(interfaces.RequirementUpload{Name: "\xd0\xe8\xc7\xf3.md", Content: []byte("x")})
			So(code, ShouldEqual, perrors.BknProject_Requirement_InvalidRequestBody)
		})

		Convey("The name is the file's base name, and a name too long is refused\n", func() {
			got, code := readUpload(interfaces.RequirementUpload{Name: `C:\docs\prd.md`, Content: []byte("x")})
			So(code, ShouldEqual, "")
			So(got.name, ShouldEqual, "prd.md")
			got, _ = readUpload(interfaces.RequirementUpload{Name: "dir/sub/prd.md", Content: []byte("x")})
			So(got.name, ShouldEqual, "prd.md")
			_, code = readUpload(interfaces.RequirementUpload{Name: strings.Repeat("名", 253) + ".md", Content: []byte("x")})
			So(code, ShouldEqual, perrors.BknProject_Requirement_InvalidRequestBody)
		})
	})
}

func Test_Documents(t *testing.T) {
	Convey("Test requirement documents\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ids := 0
		svc := &requirementService{ps: ps, ra: ra, now: func() int64 { return 100 },
			newID: func() string { ids++; return "d" + string(rune('0'+ids)) }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}

		Convey("UploadDocuments stores each file, reporting duplicates and refusals one by one\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
			var stored []*interfaces.RequirementDocument
			ra.EXPECT().CreateDocument(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, doc *interfaces.RequirementDocument) (bool, string, error) {
					stored = append(stored, doc)
					if doc.Name == "again.md" {
						return false, "d0", nil
					}
					return true, doc.ID, nil
				}).Times(2)

			results, err := svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{
				{Name: "prd.md", Content: []byte("# Goal\nAnswer.")},
				{Name: "spec.pdf", Content: []byte("%PDF")},
				{Name: "again.md", Content: []byte("same")},
			}, caller)

			So(err, ShouldBeNil)
			So(results, ShouldResemble, []interfaces.RequirementUploadResult{
				{Name: "prd.md", Outcome: interfaces.REQUIREMENT_UPLOAD_CREATED, DocumentID: "d1"},
				{Name: "spec.pdf", Outcome: interfaces.REQUIREMENT_UPLOAD_REJECTED, Code: perrors.BknProject_Requirement_UnsupportedFormat},
				{Name: "again.md", Outcome: interfaces.REQUIREMENT_UPLOAD_DUPLICATE, DocumentID: "d0"},
			})
			So(stored[0], ShouldResemble, &interfaces.RequirementDocument{
				ID: "d1", ProjectID: "p1", Name: "prd.md", DocType: interfaces.REQUIREMENT_DOC_OTHER,
				Format: interfaces.REQUIREMENT_FORMAT_MARKDOWN, Content: "# Goal\nAnswer.",
				Blocks: []interfaces.RequirementBlock{{ID: "b1", Heading: "Goal", Start: 0, End: 6},
					{ID: "b2", Heading: "Goal", Start: 7, End: 14}},
				SHA256: hashOf("# Goal\nAnswer."), Size: 14, Uploader: caller, CreateTime: 100, UpdateTime: 100,
			})
		})

		Convey("UploadDocuments refuses the files beyond the project's limit, and a repeat inside one upload\n", func() {
			existing := make([]*interfaces.RequirementDocument, MaxDocuments-1)
			for i := range existing {
				existing[i] = &interfaces.RequirementDocument{ID: "e"}
			}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return(existing, nil)
			ra.EXPECT().CreateDocument(gomock.Any(), gomock.Any()).Return(true, "d1", nil)

			results, err := svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{
				{Name: "a.md", Content: []byte("a")},
				{Name: "b.md", Content: []byte("b")},
			}, caller)

			So(err, ShouldBeNil)
			So(results[0].Outcome, ShouldEqual, interfaces.REQUIREMENT_UPLOAD_CREATED)
			So(results[1], ShouldResemble, interfaces.RequirementUploadResult{Name: "b.md",
				Outcome: interfaces.REQUIREMENT_UPLOAD_REJECTED, Code: perrors.BknProject_Requirement_TooManyDocuments})
		})

		Convey("A document the project already has is a duplicate even at the limit\n", func() {
			existing := make([]*interfaces.RequirementDocument, MaxDocuments)
			for i := range existing {
				existing[i] = &interfaces.RequirementDocument{ID: "e" + string(rune('a'+i)), SHA256: "other"}
			}
			existing[3].SHA256 = hashOf("again")
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return(existing, nil)

			results, err := svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{
				{Name: "again.md", Content: []byte("again")},
				{Name: "new.md", Content: []byte("new")},
			}, caller)

			So(err, ShouldBeNil)
			So(results, ShouldResemble, []interfaces.RequirementUploadResult{
				{Name: "again.md", Outcome: interfaces.REQUIREMENT_UPLOAD_DUPLICATE, DocumentID: "ed"},
				{Name: "new.md", Outcome: interfaces.REQUIREMENT_UPLOAD_REJECTED, Code: perrors.BknProject_Requirement_TooManyDocuments},
			})
		})

		Convey("A duplicate does not count against the limit\n", func() {
			existing := make([]*interfaces.RequirementDocument, MaxDocuments-1)
			for i := range existing {
				existing[i] = &interfaces.RequirementDocument{ID: "e"}
			}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return(existing, nil)
			ra.EXPECT().CreateDocument(gomock.Any(), gomock.Any()).Return(false, "e", nil)
			ra.EXPECT().CreateDocument(gomock.Any(), gomock.Any()).Return(true, "d2", nil)

			results, err := svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{
				{Name: "a.md", Content: []byte("a")},
				{Name: "b.md", Content: []byte("b")},
			}, caller)

			So(err, ShouldBeNil)
			So(results[0].Outcome, ShouldEqual, interfaces.REQUIREMENT_UPLOAD_DUPLICATE)
			So(results[1].Outcome, ShouldEqual, interfaces.REQUIREMENT_UPLOAD_CREATED)
		})

		Convey("Only the owner of an unarchived project changes documents\n", func() {
			other := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: interfaces.AccountInfo{ID: "u2", Type: "user"}}}
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(other, nil).Times(3)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)

			_, err := svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{{Name: "a.md", Content: []byte("a")}}, caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
			_, err = svc.SetDocumentType(ctx, token, "p1", "d1", interfaces.REQUIREMENT_DOC_PRD, caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
			err = svc.DeleteDocument(ctx, token, "p1", "d1", caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
			_, err = svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{{Name: "a.md", Content: []byte("a")}}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_ProjectArchived)
		})

		Convey("A project the caller cannot see is refused as the project service refuses it\n", func() {
			notFound := httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(nil, notFound).Times(2)

			_, err := svc.ListDocuments(ctx, token, "p1")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			_, err = svc.UploadDocuments(ctx, token, "p1", nil, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
		})

		Convey("Anyone who sees the project reads its documents, also when it is archived\n", func() {
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil).Times(3)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{{ID: "d1"}}, nil)
			ra.EXPECT().GetDocument(gomock.Any(), "p1", "d1").Return(&interfaces.RequirementDocument{ID: "d1", Content: "x"}, true, nil)
			ra.EXPECT().GetDocument(gomock.Any(), "p1", "d2").Return(nil, false, nil)

			docs, err := svc.ListDocuments(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(docs, ShouldHaveLength, 1)
			doc, err := svc.GetDocument(ctx, token, "p1", "d1")
			So(err, ShouldBeNil)
			So(doc.Content, ShouldEqual, "x")
			_, err = svc.GetDocument(ctx, token, "p1", "d2")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
		})

		Convey("SetDocumentType checks the type and the document\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).Times(3)
			ra.EXPECT().SetDocumentType(gomock.Any(), "p1", "d1", interfaces.REQUIREMENT_DOC_POLICY, int64(100)).Return(true, nil)
			ra.EXPECT().GetDocument(gomock.Any(), "p1", "d1").Return(&interfaces.RequirementDocument{ID: "d1",
				DocType: interfaces.REQUIREMENT_DOC_POLICY, Content: "x"}, true, nil)
			ra.EXPECT().SetDocumentType(gomock.Any(), "p1", "d9", interfaces.REQUIREMENT_DOC_PRD, int64(100)).Return(false, nil)

			doc, err := svc.SetDocumentType(ctx, token, "p1", "d1", interfaces.REQUIREMENT_DOC_POLICY, caller)
			So(err, ShouldBeNil)
			So(doc.DocType, ShouldEqual, interfaces.REQUIREMENT_DOC_POLICY)
			So(doc.Content, ShouldEqual, "")
			So(doc.Blocks, ShouldBeNil)
			_, err = svc.SetDocumentType(ctx, token, "p1", "d1", "manual", caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidDocumentType)
			_, err = svc.SetDocumentType(ctx, token, "p1", "d9", interfaces.REQUIREMENT_DOC_PRD, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
		})

		Convey("DeleteDocument removes the project's document, and 404 when it has none\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).Times(2)
			ra.EXPECT().DeleteDocument(gomock.Any(), "p1", "d1").Return(true, nil)
			ra.EXPECT().DeleteDocument(gomock.Any(), "p1", "d2").Return(false, nil)

			So(svc.DeleteDocument(ctx, token, "p1", "d1", caller), ShouldBeNil)
			shouldBeHTTPError(svc.DeleteDocument(ctx, token, "p1", "d2", caller), http.StatusNotFound,
				perrors.BknProject_Requirement_DocumentNotFound)
		})

		Convey("Storage errors are internal errors\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return(nil, errors.New("db down"))

			_, err := svc.UploadDocuments(ctx, token, "p1", []interfaces.RequirementUpload{{Name: "a.md", Content: []byte("a")}}, caller)
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
		})
	})
}
