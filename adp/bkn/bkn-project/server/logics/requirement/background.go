// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// networkSkillFile is the network's own SKILL.md, at the root of the definitions on a branch.
const networkSkillFile = "SKILL.md"

// maxBackgroundRunes bounds the network's background a run reads, in characters, beside the
// uploaded documents maxInputRunes bounds: a longer one is cut.
const maxBackgroundRunes = 20000

// networkBackground is the network's own background as a document of the project, read only: the
// network's description and the SKILL.md at the root of the project's branch. It is nil when the
// network has neither, as a new network does, so a project on one reads as before.
func (s *requirementService) networkBackground(ctx context.Context, token string,
	entry *interfaces.ProjectEntry) (*interfaces.RequirementDocument, error) {
	if entry == nil || entry.Project == nil {
		return nil, nil
	}
	parts := []string{}
	name := ""
	if s.bba != nil {
		network, err := s.bba.GetKN(ctx, token, entry.KNID)
		if err != nil {
			return nil, err
		}
		name = network.Name
		if description := strings.TrimSpace(network.Comment); description != "" {
			parts = append(parts, description)
		}
	}
	if s.gs != nil && entry.Branch != "" {
		files, err := s.gs.ReadTree(ctx, entry.KNID, "refs/heads/"+entry.Branch)
		if err != nil {
			return nil, err
		}
		if skill := strings.TrimSpace(withoutFrontmatter(string(files[networkSkillFile]))); skill != "" {
			parts = append(parts, skill)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	content := strings.Join(parts, "\n\n")
	if runes := []rune(content); len(runes) > maxBackgroundRunes {
		content = string(runes[:maxBackgroundRunes])
	}
	sum := sha256.Sum256([]byte(content))
	return &interfaces.RequirementDocument{
		ID: interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID, ProjectID: entry.ID, Name: name,
		DocType: interfaces.REQUIREMENT_DOC_OTHER, Format: interfaces.REQUIREMENT_FORMAT_MARKDOWN,
		Content: content, Blocks: segment(content, interfaces.REQUIREMENT_FORMAT_MARKDOWN),
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)),
	}, nil
}

// withoutFrontmatter is a Markdown file without the YAML frontmatter it starts with, if any.
func withoutFrontmatter(text string) string {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(text, "\ufeff"), "---\n")
	if !ok {
		return text
	}
	if i := strings.Index(rest, "\n---"); i >= 0 {
		after := rest[i+len("\n---"):]
		if nl := strings.IndexByte(after, '\n'); nl >= 0 {
			return after[nl+1:]
		}
		return ""
	}
	return text
}

// withBackground is what a run reads and cites: the uploaded documents, then the network's
// background when it has one. A background that cannot be read leaves the run to the documents.
func (s *requirementService) withBackground(ctx context.Context, token string, entry *interfaces.ProjectEntry,
	docs []*interfaces.RequirementDocument) []*interfaces.RequirementDocument {
	background, err := s.networkBackground(ctx, token, entry)
	if err != nil {
		logger.Warnf("Read the network background of project %s failed: %v", entry.ID, err)
		return docs
	}
	if background == nil {
		return docs
	}
	return append(append([]*interfaces.RequirementDocument{}, docs...), background)
}

// getNetworkDocument returns the network's background, or the 404 of a missing document when the
// network has none.
func (s *requirementService) getNetworkDocument(ctx context.Context, token string,
	entry *interfaces.ProjectEntry) (*interfaces.RequirementDocument, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetNetworkBackground")
	defer span.End()

	background, err := s.networkBackground(ctx, token, entry)
	if err != nil {
		span.SetStatus(codes.Error, "Read network background failed")
		return nil, internalError(ctx, "Read network background failed", err)
	}
	if background == nil {
		span.SetStatus(codes.Error, "Document not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_DocumentNotFound)
	}
	span.SetStatus(codes.Ok, "")
	return background, nil
}
