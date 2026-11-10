// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Reserved branches of a versioned network: the draft, and one immutable branch per version.
const (
	DRAFT_BRANCH          = "draft"
	VERSION_BRANCH_PREFIX = "v"
)

// VersionBranch is the branch that stores version n.
func VersionBranch(n int) string { return VERSION_BRANCH_PREFIX + strconv.Itoa(n) }

// Versioning modes of a network. A network without a state row is not versioned.
const (
	KNVersioningEnabled   = "enabled"
	KNVersioningDisabling = "disabling"
)

// Version statuses.
const (
	KNVersionRunning    = "running"
	KNVersionHistory    = "history"
	KNVersionDeprecated = "deprecated"
	KNVersionPurged     = "purged"
)

// How a version came to be.
const (
	KNVersionOriginEnable  = "enable"
	KNVersionOriginPublish = "publish"
)

// Version event types.
const (
	KNVersionEventEnable    = "enable"
	KNVersionEventPublish   = "publish"
	KNVersionEventSwitch    = "switch"
	KNVersionEventCopy      = "copy"
	KNVersionEventDiscard   = "discard"
	KNVersionEventDeprecate = "deprecate"
	KNVersionEventRestore   = "restore"
	KNVersionEventPurge     = "purge"
	KNVersionEventDisable   = "disable"

	// KNVersionReasonRetention marks the purge a Community edition publish runs.
	KNVersionReasonRetention = "retention"
)

// KNVersionState is the version state row of a versioned network.
type KNVersionState struct {
	KNID                string
	Mode                string
	RunningVersion      int
	PreviousVersion     int
	NextVersion         int
	DraftBaseVersion    int
	DraftBasedOnRunning int
	DraftModified       bool
	DraftRevision       int64
	DraftIndexBuild     string
	DraftIndexRevision  int64
	EnabledBy           string
	EnabledByType       string
	EnableTime          int64
	UpdateTime          int64
}

// DraftOutdated reports that the running version was switched after the draft took its base.
func (s *KNVersionState) DraftOutdated() bool {
	return s.RunningVersion != s.DraftBasedOnRunning
}

// DraftHasChanges reports whether the draft differs from the running version by its base and
// modified flag; content is not compared.
func (s *KNVersionState) DraftHasChanges() bool {
	return s.DraftModified || s.DraftBaseVersion != s.RunningVersion
}

// KNVersioningView is the version state returned by the API.
type KNVersioningView struct {
	Enabled         bool         `json:"enabled"`
	Mode            string       `json:"mode,omitempty"`
	RunningVersion  int          `json:"running_version"`
	PreviousVersion int          `json:"previous_version"`
	FullHistory     bool         `json:"full_history"`
	Draft           *KNDraftView `json:"draft,omitempty"`
}

type KNDraftView struct {
	BaseVersion int               `json:"base_version"`
	HasChanges  bool              `json:"has_changes"`
	Outdated    bool              `json:"outdated"`
	Revision    int64             `json:"revision"`
	Index       *KNDraftIndexView `json:"index"`
	IndexReady  bool              `json:"index_ready"`
}

type KNDraftIndexView struct {
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
}

// KNVersioningField is the versioning field of a network: a boolean in a create request, the
// version state in a response.
type KNVersioningField struct {
	Requested bool
	View      *KNVersioningView
}

func (f *KNVersioningField) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &f.Requested); err == nil {
		return nil
	}
	f.View = &KNVersioningView{}
	return json.Unmarshal(data, f.View)
}

func (f KNVersioningField) MarshalJSON() ([]byte, error) {
	if f.View != nil {
		return json.Marshal(f.View)
	}
	return json.Marshal(f.Requested)
}

// KNVersionSource records where a published version came from, such as a BKN-project commit.
type KNVersionSource struct {
	Type      string `json:"type,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

// KNVersion is one version of a network. Its content lives on branch v<Version>.
type KNVersion struct {
	KNID          string           `json:"kn_id"`
	Version       int              `json:"version"`
	Label         string           `json:"label"`
	Status        string           `json:"status"`
	Origin        string           `json:"origin"`
	BaseVersion   int              `json:"base_version"`
	DraftRevision int64            `json:"draft_revision"`
	IndexBuild    string           `json:"-"`
	Notes         string           `json:"notes"`
	Source        *KNVersionSource `json:"source,omitempty"`
	Publisher     AccountInfo      `json:"publisher"`
	PublishTime   int64            `json:"publish_time"`
	StatusBy      AccountInfo      `json:"status_by"`
	StatusTime    int64            `json:"status_time"`
}

// KNVersionEvent records one version operation.
type KNVersionEvent struct {
	ID             string      `json:"id"`
	KNID           string      `json:"kn_id"`
	Type           string      `json:"type"`
	FromVersion    int         `json:"from_version"`
	ToVersion      int         `json:"to_version"`
	Reason         string      `json:"reason,omitempty"`
	Notes          string      `json:"notes"`
	Operator       AccountInfo `json:"operator"`
	CreateTime     int64       `json:"create_time"`
	IdempotencyKey string      `json:"-"`
}

type KNEnableVersioningRequest struct {
	Label          string `json:"label"`
	Notes          string `json:"notes"`
	IdempotencyKey string `json:"-"`
}

type KNPublishRequest struct {
	ExpectedRunningVersion *int             `json:"expected_running_version"`
	ExpectedDraftRevision  *int64           `json:"expected_draft_revision"`
	Label                  string           `json:"label"`
	Notes                  string           `json:"notes"`
	Source                 *KNVersionSource `json:"source"`
	AllowOutdatedBase      bool             `json:"allow_outdated_base"`
	FailOnSkippedSources   bool             `json:"fail_on_skipped_sources"`
	IdempotencyKey         string           `json:"-"`
}

// KNPublishResult is the publish response: the new version, and the version the Community
// edition purged to keep only one previous version.
type KNPublishResult struct {
	*KNVersion
	PurgedVersion int `json:"purged_version,omitempty"`
}

type KNActivateRequest struct {
	ExpectedRunningVersion *int   `json:"expected_running_version"`
	Notes                  string `json:"notes"`
	IdempotencyKey         string `json:"-"`
}

// KNResetDraftRequest copies a version into the draft. Without FromVersion it discards the
// draft's changes, restoring the running version.
type KNResetDraftRequest struct {
	FromVersion           *int   `json:"from_version"`
	ExpectedDraftRevision *int64 `json:"expected_draft_revision"`
	OverwriteChanges      bool   `json:"overwrite_changes"`
	IdempotencyKey        string `json:"-"`
}

type KNVersionStatusRequest struct {
	Notes          string `json:"notes"`
	IdempotencyKey string `json:"-"`
}

type KNVersionsQuery struct {
	Statuses []string
	Offset   int
	Limit    int
}

// KNBranchRequest is how a request names what it reads or writes: the legacy branch
// parameter, and kn_version with draft_revision.
type KNBranchRequest struct {
	Branch        string
	KNVersion     string
	DraftRevision *int64
	Write         bool
}

// KNResolvedBranch is the storage branch a request reads or writes. Version is the value of
// the X-Kn-Version response header, empty when none is sent.
type KNResolvedBranch struct {
	Branch string
	// IndexBranch is the branch of the concept index to search. Until versions have concept
	// index builds of their own, the running version is searched in the index of main, which
	// has the same content.
	IndexBranch   string
	Versioned     bool
	Version       string
	DraftRevision int64
}

// KNVersionService manages the versions of knowledge networks.
type KNVersionService interface {
	// ResolveBranch maps a request's branch and kn_version to the storage branch (design §10).
	ResolveBranch(ctx context.Context, knID string, req KNBranchRequest) (*KNResolvedBranch, error)
	Enable(ctx context.Context, knID string, req *KNEnableVersioningRequest) (*KNVersioningView, error)
	GetState(ctx context.Context, knID string) (*KNVersioningView, error)
	ListVersions(ctx context.Context, knID string, query *KNVersionsQuery) ([]*KNVersion, int, error)
	GetVersion(ctx context.Context, knID string, version int) (*KNVersion, error)
	Publish(ctx context.Context, knID string, req *KNPublishRequest) (*KNPublishResult, error)
	Activate(ctx context.Context, knID string, version int, req *KNActivateRequest) (*KNVersioningView, error)
	ResetDraft(ctx context.Context, knID string, req *KNResetDraftRequest) (*KNVersioningView, error)
	Deprecate(ctx context.Context, knID string, version int, req *KNVersionStatusRequest) (*KNVersion, error)
	Restore(ctx context.Context, knID string, version int, req *KNVersionStatusRequest) (*KNVersion, error)
	Purge(ctx context.Context, knID string, version int, req *KNVersionStatusRequest) (*KNVersion, error)
	ListEvents(ctx context.Context, knID string, offset, limit int) ([]*KNVersionEvent, int, error)
	// Disable takes the network out of versioning (design §12.9): main keeps the running
	// version, the draft and every version branch are deleted, version records are kept.
	Disable(ctx context.Context, knID string, req *KNDisableVersioningRequest) (*KNVersioningView, error)
}

// KNDisableVersioningRequest is the body of the operator's disable request.
type KNDisableVersioningRequest struct {
	DiscardDraft bool   `json:"discard_draft"`
	Notes        string `json:"notes"`
}

// KNVersionAccess stores version state, versions and version events. Methods that take a tx
// run inside it; reads with a nil tx run on the database directly.
type KNVersionAccess interface {
	// LockNetwork locks the main row of the network, exclusively or in share mode, and reports
	// whether the network exists.
	LockNetwork(ctx context.Context, tx *sql.Tx, knID string, exclusive bool) (bool, error)
	// GetState returns the version state, or nil when the network is not versioned.
	GetState(ctx context.Context, tx *sql.Tx, knID string, forUpdate bool) (*KNVersionState, error)
	CreateState(ctx context.Context, tx *sql.Tx, state *KNVersionState) error
	UpdateState(ctx context.Context, tx *sql.Tx, state *KNVersionState) error
	// MarkDraftWritten adds one to the draft revision and marks the draft modified. With
	// expected set, it changes nothing unless the revision equals it, and returns ok false.
	MarkDraftWritten(ctx context.Context, tx *sql.Tx, knID string, expected *int64) (revision int64, ok bool, err error)

	MaxVersion(ctx context.Context, tx *sql.Tx, knID string) (int, error)
	CreateVersion(ctx context.Context, tx *sql.Tx, version *KNVersion) error
	GetVersion(ctx context.Context, tx *sql.Tx, knID string, version int, forUpdate bool) (*KNVersion, error)
	ListVersions(ctx context.Context, knID string, query *KNVersionsQuery) ([]*KNVersion, int, error)
	UpdateVersionStatus(ctx context.Context, tx *sql.Tx, version *KNVersion) error

	CreateEvent(ctx context.Context, tx *sql.Tx, event *KNVersionEvent) error
	GetEventByIdempotencyKey(ctx context.Context, tx *sql.Tx, knID, key string) (*KNVersionEvent, error)
	ListEvents(ctx context.Context, knID string, offset, limit int) ([]*KNVersionEvent, int, error)

	// DeleteNetwork removes every version row of a deleted network.
	DeleteNetwork(ctx context.Context, tx *sql.Tx, knID string) error
	// DeleteState removes the version state row, after which the network is not versioned.
	DeleteState(ctx context.Context, tx *sql.Tx, knID string) error

	// DefinitionRevision returns the revision of one definition, locking its row, and false
	// when the definition does not exist. kind is a RevisionKind.
	DefinitionRevision(ctx context.Context, tx *sql.Tx, kind, knID, branch, id string) (string, bool, error)
	// TouchDefinitionRevision gives one definition a new revision, for changes stored outside
	// its own row, such as concept group members.
	TouchDefinitionRevision(ctx context.Context, tx *sql.Tx, kind, knID, branch, id string) error
	// DefinitionRevisions returns the revisions of the given definitions by id.
	DefinitionRevisions(ctx context.Context, kind, knID, branch string, ids []string) (map[string]string, error)
}

// Kinds of definitions that carry a revision, the value compared with If-Match.
const (
	RevisionKindObjectType        = MODULE_TYPE_OBJECT_TYPE
	RevisionKindRelationType      = MODULE_TYPE_RELATION_TYPE
	RevisionKindActionType        = MODULE_TYPE_ACTION_TYPE
	RevisionKindConceptGroup      = MODULE_TYPE_CONCEPT_GROUP
	RevisionKindValueType         = MODULE_TYPE_VALUE_TYPE
	RevisionKindRiskType          = MODULE_TYPE_RISK_TYPE
	RevisionKindCapabilityBinding = "capability_binding"
)

// NewDefinitionRevision returns a new revision for an inserted or changed definition.
func NewDefinitionRevision() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// Revisioned is a definition whose revision is returned on reads.
type Revisioned interface {
	RevisionID() string
	GetRevision() string
	SetRevision(revision string)
}

// BranchCopier copies the versioned rows of a network between branches, inside the caller's
// transaction.
type BranchCopier interface {
	// ReplaceBranch deletes every versioned row of dst and copies src into it. The network row
	// of dst is updated in place, never deleted, because the main row doubles as the network lock.
	ReplaceBranch(ctx context.Context, tx *sql.Tx, knID, src, dst string) error
	// DeleteBranch deletes every versioned row of the branch, its network row included.
	DeleteBranch(ctx context.Context, tx *sql.Tx, knID, branch string) error
	// CopyNetworkRow copies only the network row of src to dst.
	CopyNetworkRow(ctx context.Context, tx *sql.Tx, knID, src, dst string) error
	// BranchesWithData lists the branches of a network that have a network row or any
	// versioned row, so enabling can refuse reserved branches that already hold data.
	BranchesWithData(ctx context.Context, tx *sql.Tx, knID string) ([]string, error)
}

// KNMainPublisher replaces the content of main with another branch inside one transaction, and
// keeps the network's proxy grants in step with the new main the way a whole-network import
// does. mutate runs inside the transaction and performs the copy.
type KNMainPublisher interface {
	PublishKNMain(ctx context.Context, knID, srcBranch string, mutate func(context.Context, *sql.Tx) error) error
	// ReplaceKNDraft runs mutate, which replaces the draft, in one transaction, and keeps the
	// draft's concept index in step with it.
	ReplaceKNDraft(ctx context.Context, knID string, mutate func(context.Context, *sql.Tx) error) error
}
