// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Limits of a project's requirement documents, which the upload route also bounds what it reads
// by. Drafting reads every document in one message, so a document is kept to what a reader would
// write by hand.
const (
	MaxDocumentBytes = 1 << 20
	MaxDocuments     = 20
	maxNameRunes     = 255
)

// formats are the file extensions accepted, by format. PDF and Word documents are parsed by the
// knowledge base extension, not here.
var formats = map[string]string{
	".md":       interfaces.REQUIREMENT_FORMAT_MARKDOWN,
	".markdown": interfaces.REQUIREMENT_FORMAT_MARKDOWN,
	".txt":      interfaces.REQUIREMENT_FORMAT_TEXT,
}

// upload is an uploaded file read as a document: its base name, format and normalized text.
type upload struct {
	name    string
	format  string
	content string
	sha256  string
}

// readUpload checks one file and normalizes its text: a byte order mark is dropped and line ends
// become \n, so the same text saved on another system is the same document. It returns the error
// code of a refused file.
func readUpload(file interfaces.RequirementUpload) (*upload, string) {
	// Browsers on Windows may send the full path.
	name := path.Base(strings.ReplaceAll(file.Name, `\`, "/"))
	format, ok := formats[strings.ToLower(path.Ext(name))]
	if !ok || name == "." || name == "/" {
		return nil, perrors.BknProject_Requirement_UnsupportedFormat
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return nil, perrors.BknProject_Requirement_InvalidRequestBody
	}
	if len(file.Content) > MaxDocumentBytes {
		return nil, perrors.BknProject_Requirement_DocumentTooLarge
	}
	content := bytes.TrimPrefix(file.Content, []byte("\ufeff"))
	if len(bytes.TrimSpace(content)) == 0 || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, perrors.BknProject_Requirement_NotText
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(content), "\r\n", "\n"), "\r", "\n")
	sum := sha256.Sum256([]byte(text))
	return &upload{name: name, format: format, content: text, sha256: hex.EncodeToString(sum[:])}, ""
}

// segment splits a document into blocks: runs of non-blank lines, with a Markdown heading always
// a block of its own. Each block records the nearest heading at or above it, and its offsets in
// characters, which quotes are located by.
func segment(content string, format string) []interfaces.RequirementBlock {
	blocks := []interfaces.RequirementBlock{}
	heading := ""
	start, end := -1, -1 // the open block, in characters
	closeBlock := func() {
		if start >= 0 {
			blocks = append(blocks, interfaces.RequirementBlock{
				ID: fmt.Sprintf("b%d", len(blocks)+1), Heading: heading, Start: start, End: end,
			})
		}
		start = -1
	}
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		text := strings.TrimSuffix(line, "\n")
		length := utf8.RuneCountInString(text)
		switch {
		case strings.TrimFunc(text, unicode.IsSpace) == "":
			closeBlock()
		case format == interfaces.REQUIREMENT_FORMAT_MARKDOWN && markdownHeading(text) != "":
			closeBlock()
			heading = markdownHeading(text)
			start, end = offset, offset+length
			closeBlock()
		default:
			if start < 0 {
				start = offset
			}
			end = offset + length
		}
		offset += utf8.RuneCountInString(line)
	}
	closeBlock()
	return blocks
}

// markdownHeading returns the text of an ATX heading line, # to ######, and "" for any other line.
func markdownHeading(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return ""
	}
	marks := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	if marks < 1 || marks > 6 {
		return ""
	}
	rest := trimmed[marks:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rest), "#"))
}

func (s *requirementService) UploadDocuments(ctx context.Context, token string, projectID string,
	files []interfaces.RequirementUpload, caller interfaces.AccountInfo) ([]interfaces.RequirementUploadResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UploadRequirementDocuments")
	defer span.End()

	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	existing, err := s.ra.ListDocuments(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "List documents failed")
		return nil, internalError(ctx, "List documents failed", err)
	}
	count := len(existing)
	results := make([]interfaces.RequirementUploadResult, 0, len(files))
	for _, file := range files {
		read, code := readUpload(file)
		if code != "" {
			results = append(results, interfaces.RequirementUploadResult{Name: file.Name,
				Outcome: interfaces.REQUIREMENT_UPLOAD_REJECTED, Code: code})
			continue
		}
		if count >= MaxDocuments {
			results = append(results, interfaces.RequirementUploadResult{Name: read.name,
				Outcome: interfaces.REQUIREMENT_UPLOAD_REJECTED, Code: perrors.BknProject_Requirement_TooManyDocuments})
			continue
		}
		now := s.now()
		doc := &interfaces.RequirementDocument{
			ID: s.newID(), ProjectID: projectID, Name: read.name, DocType: interfaces.REQUIREMENT_DOC_OTHER,
			Format: read.format, Content: read.content, Blocks: segment(read.content, read.format),
			SHA256: read.sha256, Size: int64(len(read.content)), Uploader: caller, CreateTime: now, UpdateTime: now,
		}
		created, storedID, err := s.ra.CreateDocument(ctx, doc)
		if err != nil {
			span.SetStatus(codes.Error, "Create document failed")
			return nil, internalError(ctx, "Create document failed", err)
		}
		if !created {
			results = append(results, interfaces.RequirementUploadResult{Name: read.name,
				Outcome: interfaces.REQUIREMENT_UPLOAD_DUPLICATE, DocumentID: storedID})
			continue
		}
		count++
		results = append(results, interfaces.RequirementUploadResult{Name: read.name,
			Outcome: interfaces.REQUIREMENT_UPLOAD_CREATED, DocumentID: doc.ID})
	}
	span.SetStatus(codes.Ok, "")
	return results, nil
}

func (s *requirementService) ListDocuments(ctx context.Context, token string,
	projectID string) ([]*interfaces.RequirementDocument, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListRequirementDocuments")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	docs, err := s.ra.ListDocuments(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "List documents failed")
		return nil, internalError(ctx, "List documents failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return docs, nil
}

func (s *requirementService) GetDocument(ctx context.Context, token string, projectID string,
	documentID string) (*interfaces.RequirementDocument, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRequirementDocument")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	doc, ok, err := s.ra.GetDocument(ctx, projectID, documentID)
	if err != nil {
		span.SetStatus(codes.Error, "Get document failed")
		return nil, internalError(ctx, "Get document failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Document not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
	}
	span.SetStatus(codes.Ok, "")
	return doc, nil
}

// SetDocumentType returns the document without its content, as lists show it.
func (s *requirementService) SetDocumentType(ctx context.Context, token string, projectID string,
	documentID string, docType string, caller interfaces.AccountInfo) (*interfaces.RequirementDocument, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SetRequirementDocumentType")
	defer span.End()

	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	if !interfaces.IsRequirementDocType(docType) {
		span.SetStatus(codes.Error, "Invalid document type")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidDocumentType)
	}
	ok, err := s.ra.SetDocumentType(ctx, projectID, documentID, docType, s.now())
	if err != nil {
		span.SetStatus(codes.Error, "Set document type failed")
		return nil, internalError(ctx, "Set document type failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Document not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
	}
	doc, ok, err := s.ra.GetDocument(ctx, projectID, documentID)
	if err != nil {
		span.SetStatus(codes.Error, "Get document failed")
		return nil, internalError(ctx, "Get document failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Document not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
	}
	doc.Content, doc.Blocks = "", nil
	span.SetStatus(codes.Ok, "")
	return doc, nil
}

func (s *requirementService) DeleteDocument(ctx context.Context, token string, projectID string,
	documentID string, caller interfaces.AccountInfo) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteRequirementDocument")
	defer span.End()

	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return err
	}
	ok, err := s.ra.DeleteDocument(ctx, projectID, documentID)
	if err != nil {
		span.SetStatus(codes.Error, "Delete document failed")
		return internalError(ctx, "Delete document failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Document not found")
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}
