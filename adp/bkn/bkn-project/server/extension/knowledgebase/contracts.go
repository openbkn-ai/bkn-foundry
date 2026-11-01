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

// SourceScope identifies a selected source. Interactive K1/K2 reads use only
// the authenticated operator in the request context, never NetworkID as identity.
// NetworkID is reserved for the separately authorized published proxy path.
type SourceScope struct {
	NetworkID       string
	KnowledgeBaseID string
}

type PageQuery struct {
	Cursor string
	Limit  int
}

type KnowledgeBase struct {
	ID   string `json:"kb_id"`
	Name string `json:"name"`
}

type KnowledgeBasePage struct {
	Entries    []KnowledgeBase `json:"entries"`
	NextCursor string          `json:"next_cursor"`
}

type CatalogQuery struct {
	PageQuery
}

type FileQuery struct {
	PageQuery
	CatalogID    string
	Recursive    bool
	MimeTypes    []string
	UpdatedSince string
}

type Catalog struct {
	ID       string `json:"catalog_id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

type CatalogPage struct {
	Entries    []Catalog `json:"entries"`
	NextCursor string    `json:"next_cursor"`
}

// VersionRef always names an explicit content version, not "latest".
type VersionRef struct {
	FileID           string `json:"file_id"`
	ContentVersionID string `json:"content_version_id"`
}

type File struct {
	VersionRef
	Name        string `json:"name"`
	CatalogID   string `json:"catalog_id"`
	MimeType    string `json:"mime_type"`
	ChunkCount  int64  `json:"chunk_count"`
	Size        int64  `json:"size"`
	ParseStatus string `json:"parse_status"`
}

// FileSummary uses precisely the same filters as ListFiles. Files without a
// readable current content version are counted separately, never silently locked.
type FileSummary struct {
	FileCount          int64 `json:"file_count"`
	Size               int64 `json:"size"`
	VersionedFileCount int64 `json:"content_version_file_count"`
	ChunkCount         int64 `json:"chunk_count"`
}

type FilePage struct {
	Entries    []File `json:"entries"`
	NextCursor string `json:"next_cursor"`
}

// Citation is evidence for a proposal. Persisting Quote in a network is only
// allowed when its source catalog enables original-text retrieval. The
// source_quote column still exists when disabled, but its value is empty.
type Citation struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	VersionRef
	BlockID string `json:"block_id"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Quote   string `json:"quote,omitempty"`
}

// Span is a Unicode code-point interval in the immutable normalized text.
type Span struct {
	BlockID string `json:"block_id"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

type Chunk struct {
	ID       string   `json:"chunk_id"`
	Text     string   `json:"text"`
	Citation Citation `json:"citation"`
	// A chunk can span several blocks. Never invent one contiguous citation.
	Spans   []Span `json:"spans"`
	Located string `json:"located"`
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
	SummarizeFiles(context.Context, SourceScope, FileQuery) (FileSummary, error)
	ReadChunks(context.Context, SourceScope, VersionRef, PageQuery) (ChunkPage, error)
	Hold(context.Context, SourceScope, HoldRequest) error
	ReleaseHold(context.Context, SourceScope, string) error
}

// SampleVersion identifies one locked file/version and its knowledge base.
// Batches bind continuation to the entire ordered list, never "latest".
type SampleVersion struct {
	KnowledgeBaseID string `json:"kb_id"`
	VersionRef
}

type SampleChunk struct {
	SampleVersion
	ChunkID string `json:"chunk_id"`
	Index   int    `json:"index"`
	Text    string `json:"text"`
	Spans   []Span `json:"spans"`
	Located string `json:"located"`
}

type SamplePage struct {
	Entries    []SampleChunk `json:"entries"`
	NextCursor string        `json:"next_cursor"`
}

// SampleSource is the bounded K2 read extension, installed alongside Source.
// Implementations authenticate once and authorize every KB once per request.
// A cursor never carries credentials or cached authorization decisions.
type SampleSource interface {
	ReadSampleChunks(context.Context, []SampleVersion, PageQuery) (SamplePage, error)
}

func (UnavailableSource) ReadSampleChunks(context.Context, []SampleVersion, PageQuery) (SamplePage, error) {
	return SamplePage{}, ErrUnavailable
}

type ConceptRequest struct {
	NamingLanguage  string                        `json:"naming_language"`
	Samples         []Chunk                       `json:"samples"`
	Caller          interfaces.AccountInfo        `json:"-"`
	ExistingObjects []*interfaces.DraftObjectType `json:"-"`
}

// ConceptProposal reuses core draft types. It is untrusted input: core validates
// it and adds it to its existing candidate workflow; an extractor cannot save a
// draft or adopt a proposal. K2 will extend the document-model metadata here.
type ConceptProposal struct {
	ObjectTypes   []*interfaces.DraftObjectType   `json:"object_types"`
	RelationTypes []*interfaces.DraftRelationType `json:"relation_types"`
	Evidence      []ConceptEvidence               `json:"evidence,omitempty"`
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
func (UnavailableSource) SummarizeFiles(context.Context, SourceScope, FileQuery) (FileSummary, error) {
	return FileSummary{}, ErrUnavailable
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
