// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package buildgrant enforces exact, revocable delegation for project jobs.
// It never materializes general resource permissions for a build principal.
package buildgrant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ActionRead          = "read"
	ActionCreateHold    = "create_hold"
	ActionCleanup       = "cleanup"
	StatusActive        = "active"
	StatusRevoked       = "revoked"
	StatusCleaned       = "cleaned"
	ManagedResourceType = "bkn_project_build"
	MaxVersions         = 1000
)

var (
	ErrInvalid         = errors.New("invalid build delegation")
	ErrForbidden       = errors.New("build delegation denied")
	ErrConflict        = errors.New("build delegation conflicts with an immutable or revoked scope")
	idPattern          = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
	fingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Version struct {
	KnowledgeBaseID  string `json:"kb_id"`
	FileID           string `json:"file_id"`
	ContentVersionID string `json:"content_version_id"`
}
type CreateRequest struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	BuildID          string    `json:"build_id"`
	DelegatorID      string    `json:"delegator_id"`
	ModelFingerprint string    `json:"model_fingerprint"`
	Versions         []Version `json:"versions"`
}
type Grant struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	BuildID      string `json:"build_id"`
	PrincipalID  string `json:"principal_id"`
	Revision     uint64 `json:"revision"`
	Status       string `json:"status"`
	ManifestHash string `json:"manifest_hash"`
}
type CheckRequest struct {
	GrantID     string    `json:"grant_id"`
	ProjectID   string    `json:"project_id"`
	BuildID     string    `json:"build_id"`
	PrincipalID string    `json:"principal_id"`
	Revision    uint64    `json:"revision"`
	Action      string    `json:"action"`
	Versions    []Version `json:"versions,omitempty"`
}
type PermissionChecker interface {
	CheckResourceOpsScoped(context.Context, string, []authz.ResourceOperationCheck, authz.EvaluationScope) ([]authz.FilteredResource, error)
}
type Service struct {
	db      *gorm.DB
	checker PermissionChecker
}

func New(db *gorm.DB, checker PermissionChecker) *Service { return &Service{db: db, checker: checker} }
func validID(id string, max int) bool {
	return len(id) > 0 && len(id) <= max && idPattern.MatchString(id)
}
func canonical(versions []Version) ([]Version, string, error) {
	if len(versions) == 0 || len(versions) > MaxVersions {
		return nil, "", ErrInvalid
	}
	copied := append([]Version(nil), versions...)
	sort.Slice(copied, func(i, j int) bool {
		a, b := copied[i], copied[j]
		if a.KnowledgeBaseID != b.KnowledgeBaseID {
			return a.KnowledgeBaseID < b.KnowledgeBaseID
		}
		if a.FileID != b.FileID {
			return a.FileID < b.FileID
		}
		return a.ContentVersionID < b.ContentVersionID
	})
	for i, v := range copied {
		if !validID(v.KnowledgeBaseID, 128) || !validID(v.FileID, 128) || !validID(v.ContentVersionID, 128) || (i > 0 && copied[i-1].KnowledgeBaseID == v.KnowledgeBaseID && copied[i-1].FileID == v.FileID) {
			return nil, "", ErrInvalid
		}
	}
	raw, _ := json.Marshal(copied)
	sum := sha256.Sum256(raw)
	return copied, hex.EncodeToString(sum[:]), nil
}
func render(g model.BuildGrant) *Grant {
	return &Grant{ID: g.ID, ProjectID: g.ProjectID, BuildID: g.BuildID, PrincipalID: g.PrincipalID, Revision: g.Revision, Status: g.Status, ManifestHash: g.ManifestHash}
}
func (s *Service) permitted(ctx context.Context, delegator string, versions []Version) error {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", delegator).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrForbidden
		}
		return err
	}
	if !user.Enabled || (user.AccountType != model.AccountTypeOther && user.AccountType != model.AccountTypeIDCard) {
		return ErrForbidden
	}
	if s.checker == nil {
		return errors.New("authorization dependency unavailable")
	}
	checks := []authz.ResourceOperationCheck{}
	expected := map[string]bool{}
	for _, v := range versions {
		if !expected[v.KnowledgeBaseID] {
			expected[v.KnowledgeBaseID] = true
			checks = append(checks, authz.ResourceOperationCheck{Resource: authz.ResourceRef{Type: "knowledge_base", ID: v.KnowledgeBaseID}, Operations: []string{"query_data"}})
		}
	}
	results, err := s.checker.CheckResourceOpsScoped(ctx, delegator, checks, authz.ScopeEffective)
	if err != nil {
		return err
	}
	if len(results) != len(checks) {
		return errors.New("incomplete permission batch")
	}
	seen := map[string]bool{}
	for _, result := range results {
		if result.Type != "knowledge_base" || !expected[result.ID] || seen[result.ID] {
			return errors.New("inconsistent permission batch")
		}
		seen[result.ID] = true
		if !slices.Contains(result.Operations, "query_data") {
			return ErrForbidden
		}
	}
	return nil
}
func same(g model.BuildGrant, r CreateRequest, hash string) bool {
	return g.ProjectID == r.ProjectID && g.BuildID == r.BuildID && g.DelegatorID == r.DelegatorID && g.ManifestHash == hash && g.ModelFingerprint == r.ModelFingerprint && g.Status == StatusActive
}
func (s *Service) Create(ctx context.Context, r CreateRequest) (*Grant, bool, error) {
	if !validID(r.ID, 64) || !validID(r.ProjectID, 128) || !validID(r.BuildID, 128) || !validID(r.DelegatorID, 64) || !fingerprintPattern.MatchString(r.ModelFingerprint) {
		return nil, false, ErrInvalid
	}
	versions, hash, err := canonical(r.Versions)
	if err != nil {
		return nil, false, err
	}
	var existing model.BuildGrant
	err = s.db.WithContext(ctx).First(&existing, "id = ?", r.ID).Error
	if err == nil && !same(existing, r, hash) {
		return nil, false, ErrConflict
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if err = s.permitted(ctx, r.DelegatorID, versions); err != nil {
		return nil, false, err
	}
	g := model.BuildGrant{ID: r.ID, ProjectID: r.ProjectID, BuildID: r.BuildID, DelegatorID: r.DelegatorID, ModelFingerprint: r.ModelFingerprint, ManifestHash: hash, Revision: 1, Status: StatusActive, PrincipalID: uuid.NewString()}
	created := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&g)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "id = ?", r.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrConflict
				}
				return err
			}
			if !same(existing, r, hash) {
				return ErrConflict
			}
			g = existing
			return nil
		}
		user := model.User{ID: g.PrincipalID, Account: "bkn-build-" + g.PrincipalID, Name: "BKN build service", Enabled: true, Source: model.SourceLocal, AccountType: model.AccountTypeApp}
		mapping := model.ManagedProxyAccount{ProxyAccountID: g.PrincipalID, ManagedBy: "bkn", ManagedResourceType: ManagedResourceType, ManagedResourceID: r.ID, LifecycleStatus: "active", Version: 1}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		if err := tx.Create(&mapping).Error; err != nil {
			return err
		}
		rows := make([]model.BuildGrantVersion, 0, len(versions))
		for _, v := range versions {
			rows = append(rows, model.BuildGrantVersion{GrantID: g.ID, KnowledgeBaseID: v.KnowledgeBaseID, FileID: v.FileID, ContentVersionID: v.ContentVersionID})
		}
		if err := tx.CreateInBatches(rows, 100).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return render(g), created, nil
}
func (s *Service) Check(ctx context.Context, r CheckRequest) error {
	if !validID(r.GrantID, 64) || !validID(r.ProjectID, 128) || !validID(r.BuildID, 128) || !validID(r.PrincipalID, 64) || r.Revision == 0 {
		return ErrInvalid
	}
	if r.Action != ActionRead && r.Action != ActionCreateHold && r.Action != ActionCleanup {
		return ErrInvalid
	}
	if r.Action == ActionCleanup && len(r.Versions) != 0 {
		return ErrInvalid
	}
	if r.Action != ActionCleanup && (len(r.Versions) == 0 || len(r.Versions) > 20) {
		return ErrInvalid
	}
	var g model.BuildGrant
	if err := s.db.WithContext(ctx).First(&g, "id = ?", r.GrantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrForbidden
		}
		return err
	}
	if g.ProjectID != r.ProjectID || g.BuildID != r.BuildID || g.PrincipalID != r.PrincipalID || g.Revision != r.Revision || (g.Status != StatusActive && g.Status != StatusRevoked) || (r.Action != ActionCleanup && g.Status != StatusActive) {
		return ErrForbidden
	}
	var mapping model.ManagedProxyAccount
	if err := s.db.WithContext(ctx).First(&mapping, "proxy_account_id = ?", g.PrincipalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrForbidden
		}
		return err
	}
	if mapping.ManagedBy != "bkn" || mapping.ManagedResourceType != ManagedResourceType || mapping.ManagedResourceID != g.ID || mapping.LifecycleStatus != "active" || mapping.Version != 1 {
		return ErrForbidden
	}
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", g.PrincipalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrForbidden
		}
		return err
	}
	if !user.Enabled || user.AccountType != model.AccountTypeApp || user.PasswordHash != "" || user.MustChangePassword {
		return ErrForbidden
	}
	if r.Action == ActionCleanup {
		return nil
	}
	for _, v := range r.Versions {
		if !validID(v.KnowledgeBaseID, 128) || !validID(v.FileID, 128) || !validID(v.ContentVersionID, 128) {
			return ErrInvalid
		}
		var count int64
		if err := s.db.WithContext(ctx).Model(&model.BuildGrantVersion{}).Where("grant_id = ? AND knowledge_base_id = ? AND file_id = ? AND content_version_id = ?", g.ID, v.KnowledgeBaseID, v.FileID, v.ContentVersionID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrForbidden
		}
	}
	// Recheck every source in the task scope: revoking one knowledge base
	// stops the task rather than letting other pages continue independently.
	var knowledgeBases []string
	if err := s.db.WithContext(ctx).Model(&model.BuildGrantVersion{}).Where("grant_id = ?", g.ID).Distinct("knowledge_base_id").Pluck("knowledge_base_id", &knowledgeBases).Error; err != nil {
		return err
	}
	if len(knowledgeBases) == 0 {
		return ErrForbidden
	}
	scopes := make([]Version, 0, len(knowledgeBases))
	for _, id := range knowledgeBases {
		scopes = append(scopes, Version{KnowledgeBaseID: id})
	}
	return s.permitted(ctx, g.DelegatorID, scopes)
}

// Revoke creates a tombstone even if a timed-out Create has not arrived yet.
// Callers persist the private grant id before Create so cancellation can fence it.
func (s *Service) Revoke(ctx context.Context, id, project, build string) error {
	if !validID(id, 64) || !validID(project, 128) || !validID(build, 128) {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tombstone := model.BuildGrant{ID: id, ProjectID: project, BuildID: build, Revision: 1, Status: StatusRevoked}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tombstone).Error; err != nil {
			return err
		}
		var g model.BuildGrant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrConflict
			}
			return err
		}
		if g.ProjectID != project || g.BuildID != build {
			return ErrConflict
		}
		if g.Status != StatusActive {
			return nil
		}
		return tx.Model(&g).Update("status", StatusRevoked).Error
	})
}
func (s *Service) CompleteCleanup(ctx context.Context, id, project, build string) error {
	if !validID(id, 64) || !validID(project, 128) || !validID(build, 128) {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var g model.BuildGrant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrConflict
			}
			return err
		}
		if g.ProjectID != project || g.BuildID != build || (g.Status != StatusRevoked && g.Status != StatusCleaned) {
			return ErrConflict
		}
		if g.Status == StatusCleaned {
			return nil
		}
		if g.PrincipalID != "" {
			if err := tx.Model(&model.User{}).Where("id = ?", g.PrincipalID).Update("enabled", false).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.ManagedProxyAccount{}).Where("proxy_account_id = ? AND managed_resource_type = ?", g.PrincipalID, ManagedResourceType).Update("lifecycle_status", "archived").Error; err != nil {
				return err
			}
		}
		return tx.Model(&g).Update("status", StatusCleaned).Error
	})
}
