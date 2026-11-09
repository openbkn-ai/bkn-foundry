// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// The knowledge network version API of bkn-backend (bkn-foundry#1229). A versioned network keeps
// one draft, one running version and the earlier ones under one network id: every edit goes to the
// draft, a publish turns the draft into the running version, and activating switches the running
// version back to an earlier one. Error codes start with KN_VERSION_ERROR_PREFIX.

// KN_VERSION_DRAFT and KN_VERSION_RUNNING name the draft and the running version wherever
// bkn-backend takes kn_version.
const (
	KN_VERSION_DRAFT   = "draft"
	KN_VERSION_RUNNING = "running"
)

// bkn-backend error codes of the version API that bkn-project answers on its own.
const (
	KN_VERSION_ERROR_PREFIX                  = "BknBackend.KNVersion."
	KN_VERSION_ERROR_NOT_VERSIONED           = KN_VERSION_ERROR_PREFIX + "NotVersioned"
	KN_VERSION_ERROR_ALREADY_VERSIONED       = KN_VERSION_ERROR_PREFIX + "AlreadyVersioned"
	KN_VERSION_ERROR_RUNNING_MISMATCH        = KN_VERSION_ERROR_PREFIX + "RunningVersionMismatch"
	KN_VERSION_ERROR_DRAFT_REVISION_MISMATCH = KN_VERSION_ERROR_PREFIX + "DraftRevisionMismatch"
	KN_VERSION_ERROR_DRAFT_HAS_CHANGES       = KN_VERSION_ERROR_PREFIX + "DraftHasChanges"
	KN_VERSION_ERROR_DRAFT_BASE_OUTDATED     = KN_VERSION_ERROR_PREFIX + "DraftBaseOutdated"
	KN_VERSION_ERROR_NOTHING_TO_PUBLISH      = KN_VERSION_ERROR_PREFIX + "NothingToPublish"
	KN_VERSION_ERROR_VALIDATION_FAILED       = KN_VERSION_ERROR_PREFIX + "ValidationFailed"
	KN_VERSION_ERROR_BROKEN_REFERENCES       = KN_VERSION_ERROR_PREFIX + "BrokenReferences"
	KN_VERSION_ERROR_NOT_ACTIVATABLE         = KN_VERSION_ERROR_PREFIX + "VersionNotActivatable"
	KN_VERSION_ERROR_EDITION_RESTRICTED      = KN_VERSION_ERROR_PREFIX + "EditionRestricted"
	KN_VERSION_ERROR_PURGED                  = KN_VERSION_ERROR_PREFIX + "VersionPurged"
	KN_VERSION_ERROR_CONCEPT_INDEX_FAILED    = KN_VERSION_ERROR_PREFIX + "ConceptIndexFailed"
	KN_VERSION_ERROR_PROXY_SYNC_REQUIRED     = KN_VERSION_ERROR_PREFIX + "ProxySyncRequired"
	KN_VERSION_ERROR_CONCEPT_INDEX_BUILDING  = KN_VERSION_ERROR_PREFIX + "ConceptIndexBuilding"
	KN_VERSION_ERROR_IDEMPOTENCY_KEY_REUSED  = KN_VERSION_ERROR_PREFIX + "IdempotencyKeyReused"
)

// KN_VERSION_WARNING_CAPABILITY_NOT_PINNED warns that switching versions does not restore the code
// of functions and Skills; bkn-project's copies are per release version, so it does not apply.
const KN_VERSION_WARNING_CAPABILITY_NOT_PINNED = "capability_not_pinned"

// Draft concept index build states.
const (
	KN_DRAFT_INDEX_BUILDING = "building"
	KN_DRAFT_INDEX_READY    = "ready"
	KN_DRAFT_INDEX_FAILED   = "failed"
)

// Version states as bkn-backend reports them.
const (
	KN_VERSION_STATE_RUNNING    = "running"
	KN_VERSION_STATE_HISTORY    = "history"
	KN_VERSION_STATE_DEPRECATED = "deprecated"
	KN_VERSION_STATE_PURGED     = "purged"
)

// KN_VERSION_SOURCE_TYPE marks a version bkn-project published.
const KN_VERSION_SOURCE_TYPE = "bkn-project"

// KNVersioning is GET /knowledge-networks/{kn_id}/versioning.
type KNVersioning struct {
	Enabled         bool `json:"enabled"`
	RunningVersion  int  `json:"running_version"`
	PreviousVersion int  `json:"previous_version"`
	// FullHistory is false in the community edition: only the running and the previous version
	// can be read or activated.
	FullHistory bool         `json:"full_history"`
	Draft       KNDraftState `json:"draft"`
}

// KNDraftState is the draft of a versioned network.
type KNDraftState struct {
	BaseVersion int  `json:"base_version"`
	HasChanges  bool `json:"has_changes"`
	// Outdated is set once the running version was switched after the draft took its base.
	Outdated bool  `json:"outdated"`
	Revision int64 `json:"revision"`
	// IndexReady is set once the concept index of the draft at Revision is built.
	IndexReady bool              `json:"index_ready"`
	Index      KNDraftIndexState `json:"index"`
}

// KNDraftIndexState is the concept index build of a draft revision.
type KNDraftIndexState struct {
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
}

// KNVersionSource is where a version came from. bkn-project fills ReleaseID with its release
// record, so a retried publish can tell its own version from someone else's.
type KNVersionSource struct {
	Type      string `json:"type,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Ref       string `json:"ref,omitempty"`
	ReleaseID string `json:"release_id,omitempty"`
}

// KNVersion is one published version.
type KNVersion struct {
	Version     int             `json:"version"`
	Label       string          `json:"label"`
	State       string          `json:"status"`
	BaseVersion int             `json:"base_version"`
	Notes       string          `json:"notes"`
	Publisher   AccountInfo     `json:"publisher"`
	CreateTime  int64           `json:"create_time"`
	Source      KNVersionSource `json:"source"`
}

// KNVersionPublish is POST /knowledge-networks/{kn_id}/versions.
type KNVersionPublish struct {
	ExpectedRunningVersion int             `json:"expected_running_version"`
	ExpectedDraftRevision  int64           `json:"expected_draft_revision"`
	Notes                  string          `json:"notes"`
	Label                  string          `json:"label,omitempty"`
	Source                 KNVersionSource `json:"source"`
	AllowOutdatedBase      bool            `json:"allow_outdated_base,omitempty"`
	// FailOnSkippedSources refuses the publish when the network's agents could not read a source
	// of the new version: the regression ran with the publisher's own permissions and cannot
	// tell.
	FailOnSkippedSources bool `json:"fail_on_skipped_sources"`
	// IdempotencyKey makes a retried publish return the first result. It is kept with the request
	// in bkn-project's release record, never sent in the body.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// KNVersionWarning is a warning a publish or an activation succeeded with.
type KNVersionWarning struct {
	Category    string `json:"category"`
	ID          string `json:"id"`
	Description string `json:"description"`
}

// KNPublishedMetric is a metric version a publish made queryable.
type KNPublishedMetric struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// KNVersionPublished is the reply of a publish.
type KNVersionPublished struct {
	KNVersion
	Warnings         []KNVersionWarning  `json:"warnings"`
	PublishedMetrics []KNPublishedMetric `json:"published_metrics"`
	// PurgedVersion is the version the community edition dropped to keep only the previous one.
	PurgedVersion int `json:"purged_version"`
}

// KNVersionActivated is the reply of an activation.
type KNVersionActivated struct {
	RunningVersion int                `json:"running_version"`
	Warnings       []KNVersionWarning `json:"warnings"`
}

// KNReplaceImport is the whole-package replace of the draft: definitions missing from the package
// are removed from the draft. Overwrite confirms dropping edits made to the draft and not
// published.
type KNReplaceImport struct {
	ExpectedDraftRevision int64
	Overwrite             bool
}

//go:generate mockgen -source ../interfaces/kn_version_access.go -destination ../interfaces/mock/mock_kn_version_access.go

// KNVersionAccess calls the version API as the caller. Errors from bkn-backend are
// *BknBackendError.
type KNVersionAccess interface {
	// CreateVersionedKN creates an empty network that is versioned from the start.
	CreateVersionedKN(ctx context.Context, token string, id string, name string) (string, error)
	// EnableVersioning turns an unversioned network versioned: its content becomes version 1.
	EnableVersioning(ctx context.Context, token string, knID string, notes string) error
	GetVersioning(ctx context.Context, token string, knID string) (*KNVersioning, error)
	// ListVersions returns every version, newest first.
	ListVersions(ctx context.Context, token string, knID string) ([]*KNVersion, error)
	GetVersion(ctx context.Context, token string, knID string, version int) (*KNVersion, error)
	// ReplaceDraft imports files into the draft with import_mode=replace, and returns the draft
	// revision the import made.
	ReplaceDraft(ctx context.Context, token string, files map[string][]byte, req KNReplaceImport) (*BKNImportResult, int64, error)
	// BuildDraftIndex starts building the concept index of the draft as it is; GetVersioning tells
	// when it is ready.
	BuildDraftIndex(ctx context.Context, token string, knID string) error
	// ExportVersion exports kn_version (draft, running or a number) as BKN 3.0 files.
	ExportVersion(ctx context.Context, token string, knID string, version string) (map[string][]byte, error)
	Publish(ctx context.Context, token string, knID string, req KNVersionPublish) (*KNVersionPublished, error)
	Activate(ctx context.Context, token string, knID string, version int, expectedRunning int, notes string) (*KNVersionActivated, error)
	// ResetDraft copies a version into the draft; overwrite confirms dropping unpublished edits.
	ResetDraft(ctx context.Context, token string, knID string, fromVersion int, expectedDraftRevision int64, overwrite bool) error
	// ReferencedResources returns the data resources an unpurged version binds.
	ReferencedResources(ctx context.Context, token string, knID string) ([]string, error)
}
