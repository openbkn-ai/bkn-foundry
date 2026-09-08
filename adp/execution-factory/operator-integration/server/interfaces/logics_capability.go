// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

//go:generate mockgen -source=logics_capability.go -destination=../mocks/logics_capability.go -package=mocks

import "context"

// Capability types. These mirror the three-part identity the knowledge network stores in
// t_kn_capability_binding, so a binding row and an index document name the same thing without a
// translation table in between.
const (
	CapabilityTypeSkill    = "skill"
	CapabilityTypeFunction = "function"
	CapabilityTypeMCPTool  = "mcp_tool"
)

// Channels a hit can be attributed to. knn and match are the two retrieval channels; hybrid means
// both channels returned the document and the fused rank decided its place.
const (
	CapabilityMatchedByKnn    = "knn"
	CapabilityMatchedByMatch  = "match"
	CapabilityMatchedByHybrid = "hybrid"
	// CapabilityMatchedByLike is the enumeration case: no query text, so nothing was matched by
	// content and the whitelist filter is the only thing that selected the row.
	CapabilityMatchedByLike = "like"
)

// CapabilityRef is one capability's identity: which kind it is, who owns it, and which one it is.
//
// OwnerID is deliberately type-neutral — a Function tool's box, an MCP tool's server, and nothing
// at all for a Skill. Naming it after any one of those would have forced a second field the day a
// third kind arrived.
type CapabilityRef struct {
	CapabilityType string `json:"capability_type"`
	OwnerID        string `json:"owner_id"`
	CapabilityID   string `json:"capability_id"`
}

// CapabilityDocument is what the index holds for one capability.
//
// The descriptive fields below name/description are a snapshot for display; the execution factory's
// own tables remain the authoritative source. Fields that do not apply to a kind are left blank
// rather than given a per-kind schema: one schema is the declaration of what a capability has.
type CapabilityDocument struct {
	CapabilityRef
	Name        string
	Description string
	Version     string
	Category    string
	CreateUser  string
	CreateTime  int64
	UpdateUser  string
	UpdateTime  int64
}

// SearchCapabilitiesReq asks for the capabilities in Refs that best answer Query.
type SearchCapabilitiesReq struct {
	Query string          `json:"query"`
	Refs  []CapabilityRef `json:"refs"`
	TopK  int             `json:"top_k"`
	// Types optionally narrows the result to certain capability types. Empty means all three.
	// It narrows within Refs and can never reach outside it.
	Types []string `json:"types"`
}

// SearchCapabilitiesResp is one page of ranked capabilities.
type SearchCapabilitiesResp struct {
	Entries []*CapabilityHit `json:"entries"`
}

// CapabilityHit is one ranked capability.
type CapabilityHit struct {
	CapabilityRef
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MatchedBy   string  `json:"matched_by"`
	Score       float64 `json:"score"`
}

// IndexedCapability is what the index already holds for one capability, without its vector.
//
// Name and description are the whole comparison the reconciler needs: they are the only fields the
// embedding is derived from, so a document whose two strings are unchanged does not need to be
// vectorised again.
type IndexedCapability struct {
	CapabilityRef
	Name        string
	Description string
}

// CapabilityIndexSyncService keeps the capability index in step with the execution factory's own
// tables.
type CapabilityIndexSyncService interface {
	Init(ctx context.Context) error
	EnsureInitialized(ctx context.Context) error
	UpsertCapability(ctx context.Context, doc *CapabilityDocument) error
	DeleteCapability(ctx context.Context, ref CapabilityRef) error
	// ListIndexed returns every document of one capability type currently in the index.
	ListIndexed(ctx context.Context, capabilityType string) ([]IndexedCapability, error)
	// ListIndexedByOwner returns one owner's documents. Reconciling many owners with ListIndexed
	// would scan the whole capability type once per owner.
	ListIndexedByOwner(ctx context.Context, capabilityType, ownerID string) ([]IndexedCapability, error)
	// DeleteOwner removes every document of one owner: an MCP Server that was deleted, or a
	// tool box that no longer exists. Vega has no delete-by-query, so this pages the owner's
	// documents back and deletes them by id.
	DeleteOwner(ctx context.Context, capabilityType, ownerID string) error
}

// CapabilitySearchService retrieves capabilities from the index, restricted to a whitelist.
type CapabilitySearchService interface {
	SearchCapabilities(ctx context.Context, req *SearchCapabilitiesReq) (*SearchCapabilitiesResp, error)
}
