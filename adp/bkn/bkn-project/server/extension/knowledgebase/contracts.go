// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var (
	// ErrUnavailable never means an empty directory or a successful extraction.
	ErrUnavailable = errors.New("knowledge base extension unavailable")
	// ErrVersionGone maps to HTTP 410 in the calling API. Never silently read
	// the latest content when a locked version has been removed or cleaned.
	ErrVersionGone = errors.New("knowledge base content version gone")
)

// SourceScope identifies the network whose managed proxy reads the source.
// The caller must authorize the project and the selected knowledge base first.
// Credentials belong to the adapter, not to persisted project inputs.
type SourceScope struct {
	NetworkID       string
	KnowledgeBaseID string
}

type PageQuery struct {
	Cursor string
	Limit  int
}

type KnowledgeBase struct {
	ID   string
	Name string
}

type KnowledgeBasePage struct {
	Entries    []KnowledgeBase
	NextCursor string
}

type CatalogQuery struct {
	PageQuery
	ParentID string
}

type FileQuery struct {
	PageQuery
	CatalogID    string
	Recursive    bool
	MimeTypes    []string
	UpdatedSince string
}

type Catalog struct {
	ID       string
	Name     string
	ParentID string
}

type CatalogPage struct {
	Entries    []Catalog
	NextCursor string
}

// VersionRef always names an explicit content version, not "latest".
type VersionRef struct {
	FileID           string
	ContentVersionID string
}

type File struct {
	VersionRef
	Name       string
	CatalogID  string
	MimeType   string
	ChunkCount int64
}

type FilePage struct {
	Entries    []File
	NextCursor string
}

// Citation is evidence for a proposal. Persisting Quote in a network is only
// allowed when its source catalog enables original-text retrieval. The
// source_quote column still exists when disabled, but its value is empty.
type Citation struct {
	VersionRef
	BlockID string
	Start   int
	End     int
	Quote   string
}

type Chunk struct {
	ID       string
	Text     string
	Citation Citation
}

type ChunkPage struct {
	Entries    []Chunk
	NextCursor string
}

// HoldRequest uses a caller-generated UUID. Retrying the same ID and version
// set is idempotent; changing the set or reusing a released ID is a conflict.
// Hold atomically retains every requested version or fails (410 if gone).
type HoldRequest struct {
	ID       string
	Versions []VersionRef
}

// Source is the K1/K2 read seam. Locking means storing the explicit versions
// returned by ListFiles and successfully holding them before reading samples.
// ReleaseHold is idempotent; a failed release must remain retryable by core.
// Adapters preserve permission, conflict and upstream failures rather than
// treating them as missing data. Change feeds belong to the later refresh step.
type Source interface {
	ListKnowledgeBases(context.Context, SourceScope, PageQuery) (KnowledgeBasePage, error)
	ListCatalogs(context.Context, SourceScope, CatalogQuery) (CatalogPage, error)
	ListFiles(context.Context, SourceScope, FileQuery) (FilePage, error)
	ReadChunks(context.Context, SourceScope, VersionRef, PageQuery) (ChunkPage, error)
	Hold(context.Context, SourceScope, HoldRequest) error
	ReleaseHold(context.Context, SourceScope, string) error
}

type ConceptRequest struct {
	NamingLanguage string
	Samples        []Chunk
}

// ConceptProposal reuses core draft types. It is untrusted input: core validates
// it and adds it to its existing candidate workflow; an extractor cannot save a
// draft or adopt a proposal. K2 will extend the document-model metadata here.
type ConceptProposal struct {
	ObjectTypes   []*interfaces.DraftObjectType
	RelationTypes []*interfaces.DraftRelationType
	Evidence      []ConceptEvidence
}

type ConceptEvidence struct {
	Kind      string
	ID        string
	Reason    string
	Citations []Citation
}

type Concepts interface {
	Extract(context.Context, ConceptRequest) (ConceptProposal, error)
}

// UnavailableSource is the community default and the assembly skeleton until K1.
type UnavailableSource struct{}

func (UnavailableSource) ListKnowledgeBases(context.Context, SourceScope, PageQuery) (KnowledgeBasePage, error) {
	return KnowledgeBasePage{}, ErrUnavailable
}

func (UnavailableSource) ListCatalogs(context.Context, SourceScope, CatalogQuery) (CatalogPage, error) {
	return CatalogPage{}, ErrUnavailable
}
func (UnavailableSource) ListFiles(context.Context, SourceScope, FileQuery) (FilePage, error) {
	return FilePage{}, ErrUnavailable
}
func (UnavailableSource) ReadChunks(context.Context, SourceScope, VersionRef, PageQuery) (ChunkPage, error) {
	return ChunkPage{}, ErrUnavailable
}
func (UnavailableSource) Hold(context.Context, SourceScope, HoldRequest) error { return ErrUnavailable }
func (UnavailableSource) ReleaseHold(context.Context, SourceScope, string) error {
	return ErrUnavailable
}

type UnavailableConcepts struct{}

func (UnavailableConcepts) Extract(context.Context, ConceptRequest) (ConceptProposal, error) {
	return ConceptProposal{}, ErrUnavailable
}
