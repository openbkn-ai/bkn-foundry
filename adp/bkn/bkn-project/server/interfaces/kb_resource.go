// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
)

var (
	ErrKBSelectionConflict = errors.New("knowledge base selection changed")
	ErrKBSamplePlan        = errors.New("invalid knowledge base sample selection")
)

// KBSourceRange is an owner's source decision, never an authorization grant.
type KBSourceRange struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	CatalogID       string   `json:"catalog_id"`
	Recursive       bool     `json:"include_subcatalogs"`
	MimeTypes       []string `json:"mime_types"`
	UpdatedSince    string   `json:"updated_since"`
	IncludeOriginal bool     `json:"include_original"`
}

// KBResourceFile stores source identity and process metadata, not extracted data.
type KBResourceFile struct {
	KnowledgeBaseID  string `json:"knowledge_base_id"`
	FileID           string `json:"file_id"`
	ContentVersionID string `json:"content_version_id"`
	CatalogID        string `json:"catalog_id"`
	Name             string `json:"name"`
	MimeType         string `json:"mime_type"`
	ChunkCount       int64  `json:"chunk_count"`
	Size             int64  `json:"size"`
	IncludeOriginal  bool   `json:"include_original"`
	SampleRole       string `json:"sample_role"`
}

func (f KBResourceFile) Key() string { return f.KnowledgeBaseID + "/" + f.FileID }

// Nil random parameters mean unspecified, not a platform-chosen default.
type KBSamplePlan struct {
	Mode              string   `json:"mode"`
	DebugIDs          []string `json:"debug_ids"`
	AcceptanceIDs     []string `json:"acceptance_ids"`
	Ratio             *float64 `json:"ratio"`
	Minimum           *int     `json:"minimum"`
	AcceptanceRatio   *float64 `json:"acceptance_ratio"`
	AcceptanceMinimum *int     `json:"acceptance_minimum"`
}

type KBHoldGroup struct {
	ID              string   `json:"id"`
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	VersionIDs      []string `json:"version_ids"`
	Released        bool     `json:"released"`
}

type KBResourceDecision struct {
	Kind    string        `json:"kind"`
	By      AccountInfo   `json:"by"`
	Time    int64         `json:"time"`
	Samples *KBSamplePlan `json:"samples,omitempty"`
}

type KBSelection struct {
	ID         string               `json:"id"`
	ProjectID  string               `json:"project_id"`
	State      string               `json:"state"`
	Revision   int64                `json:"revision"`
	Ranges     []KBSourceRange      `json:"ranges"`
	Files      []KBResourceFile     `json:"files"`
	Samples    KBSamplePlan         `json:"samples"`
	Decisions  []KBResourceDecision `json:"decisions"`
	Owner      AccountInfo          `json:"owner"`
	CreateTime int64                `json:"create_time"`
	UpdateTime int64                `json:"update_time"`
	// Anonymous hold UUIDs are release capabilities, not UI data.
	Holds      []KBHoldGroup `json:"-"`
	ExpectedID string        `json:"-"`
	InputHash  string        `json:"-"`
	Retained   bool          `json:"-"`
}

type KBResourceState struct {
	Current        *KBSelection `json:"current"`
	Pending        *KBSelection `json:"pending"`
	CleanupPending int64        `json:"cleanup_pending"`
}

// Absence from a complete range scan is not proof of global deletion.
type KBSourceChanges struct {
	SelectionID     string           `json:"selection_id"`
	Revision        int64            `json:"revision"`
	Added           []KBResourceFile `json:"added"`
	Changed         []KBResourceFile `json:"changed"`
	NoLongerInRange []KBResourceFile `json:"no_longer_in_range"`
}

type KBLockRequest struct {
	ID         string          `json:"operation_id"`
	ExpectedID string          `json:"expected_selection_id"`
	Ranges     []KBSourceRange `json:"ranges"`
}

// KBResourceAccess keeps intents durable before any remote hold write. All
// selection mutations serialize on the project row, not on a network call.
//
//go:generate mockgen -source ../interfaces/kb_resource.go -destination ../interfaces/mock/mock_kb_resource.go
type KBResourceAccess interface {
	Read(context.Context, string) (*KBResourceState, error)
	Get(context.Context, string, string) (*KBSelection, error)
	Prepare(context.Context, *KBSelection) error
	Activate(context.Context, string, string) error
	SetSamples(context.Context, string, string, int64, []KBResourceFile, KBSamplePlan) error
	Retire(context.Context, string, string) error
	Clear(context.Context, string) error
	Cleanup(context.Context, string) ([]*KBSelection, error)
	MarkReleased(context.Context, string, string, string) error
}
