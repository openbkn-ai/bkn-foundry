// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package executiongrant issues the short-lived credential a background
// execution acts with for the person who started it, after that person's own
// sign-in has lapsed. The credential is bound to one knowledge network, one
// Agent and one run, and resolves to the delegator: it carries no permission of
// its own, and every use rechecks that the delegator is still enabled and may
// still view the network.
package executiongrant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// Prefix marks an execution credential: bex_<grant id>_<secret>.
	Prefix        = "bex_"
	StatusActive  = "active"
	StatusRevoked = "revoked"
	// MaxTTL is the longest a credential lives: the longest a background
	// execution such as a scenario regression may run.
	MaxTTL = 6 * time.Hour
	// operation is what the delegator must hold on the network, now and at
	// every use: the same check bkn-agent makes before an execution.
	operation = "view_detail"
	secretLen = 32
)

var (
	ErrInvalid   = errors.New("invalid execution grant")
	ErrForbidden = errors.New("execution grant denied")
	ErrConflict  = errors.New("execution grant conflicts with an existing or revoked one")
	// grant ids carry no underscore: it separates the id from the secret.
	grantIDPattern = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
	alphabet       = []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
)

// IssueRequest asks for a credential for one run. TTLSeconds is at most MaxTTL.
type IssueRequest struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	RunID       string `json:"run_id"`
	DelegatorID string `json:"delegator_id"`
	KnID        string `json:"kn_id"`
	AgentID     string `json:"agent_id"`
	TTLSeconds  int64  `json:"ttl_seconds"`
}

// Issued is a new grant with its credential, returned once.
type Issued struct {
	ID         string    `json:"id"`
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Verified is who a credential acts for and what it is bound to.
type Verified struct {
	GrantID     string
	DelegatorID string
	Name        string
	AccountType string
	ProjectID   string
	RunID       string
	KnID        string
	AgentID     string
}

type PermissionChecker interface {
	CheckResourceOpsScoped(context.Context, string, []authz.ResourceOperationCheck, authz.EvaluationScope) ([]authz.FilteredResource, error)
}

type Service struct {
	db      *gorm.DB
	checker PermissionChecker
	now     func() time.Time
}

func New(db *gorm.DB, checker PermissionChecker) *Service {
	return &Service{db: db, checker: checker, now: time.Now}
}

func valid(id string, max int) bool {
	return len(id) > 0 && len(id) <= max && idPattern.MatchString(id)
}

func hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func newSecret() (string, error) {
	b := make([]byte, secretLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

// delegator is the person a grant acts for, when that person is still enabled,
// signs in as a person, and may view the network.
func (s *Service) delegator(ctx context.Context, id, kn string) (*model.User, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	if !user.Enabled || (user.AccountType != model.AccountTypeOther && user.AccountType != model.AccountTypeIDCard) {
		return nil, ErrForbidden
	}
	if s.checker == nil {
		return nil, errors.New("authorization dependency unavailable")
	}
	results, err := s.checker.CheckResourceOpsScoped(ctx, id, []authz.ResourceOperationCheck{{
		Resource: authz.ResourceRef{Type: "knowledge_network", ID: kn}, Operations: []string{operation}}}, authz.ScopeEffective)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 || results[0].Type != "knowledge_network" || results[0].ID != kn ||
		len(results[0].Decisions) != 1 || results[0].Decisions[0].Operation != operation {
		return nil, errors.New("inconsistent permission decision")
	}
	if results[0].Decisions[0].Decision != authz.DecisionAllow {
		return nil, ErrForbidden
	}
	return &user, nil
}

// Issue creates a grant and returns its credential once. An id already used,
// revoked included, is a conflict: the credential cannot be shown again, so a
// caller that lost the answer issues under a new id.
func (s *Service) Issue(ctx context.Context, r IssueRequest) (*Issued, error) {
	if len(r.ID) == 0 || len(r.ID) > 64 || !grantIDPattern.MatchString(r.ID) || !valid(r.ProjectID, 128) || !valid(r.RunID, 128) ||
		!valid(r.DelegatorID, 64) || !valid(r.KnID, 128) || !valid(r.AgentID, 128) ||
		r.TTLSeconds <= 0 || time.Duration(r.TTLSeconds)*time.Second > MaxTTL {
		return nil, ErrInvalid
	}
	if _, err := s.delegator(ctx, r.DelegatorID, r.KnID); err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, err
	}
	expires := s.now().Add(time.Duration(r.TTLSeconds) * time.Second).UTC()
	g := model.ExecutionGrant{ID: r.ID, ProjectID: r.ProjectID, RunID: r.RunID, DelegatorID: r.DelegatorID, KnID: r.KnID,
		AgentID: r.AgentID, SecretHash: hash(secret), ExpiresAt: expires, Status: StatusActive}
	claim := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&g)
	if claim.Error != nil {
		return nil, claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil, ErrConflict
	}
	return &Issued{ID: g.ID, Credential: Prefix + g.ID + "_" + secret, ExpiresAt: expires}, nil
}

// Verify resolves a credential to the delegator it acts for. It fails when the
// credential is malformed or unknown, the grant is revoked or expired, the
// secret does not match, or the delegator is disabled or may no longer view the
// network.
func (s *Service) Verify(ctx context.Context, credential string) (*Verified, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(credential), Prefix)
	if !ok {
		return nil, ErrForbidden
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" || len(id) > 64 || !grantIDPattern.MatchString(id) {
		return nil, ErrForbidden
	}
	var g model.ExecutionGrant
	if err := s.db.WithContext(ctx).First(&g, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	if g.Status != StatusActive || !s.now().Before(g.ExpiresAt) || g.SecretHash == "" ||
		subtle.ConstantTimeCompare([]byte(hash(secret)), []byte(g.SecretHash)) != 1 {
		return nil, ErrForbidden
	}
	user, err := s.delegator(ctx, g.DelegatorID, g.KnID)
	if err != nil {
		return nil, err
	}
	return &Verified{GrantID: g.ID, DelegatorID: user.ID, Name: user.Name, AccountType: string(user.AccountType),
		ProjectID: g.ProjectID, RunID: g.RunID, KnID: g.KnID, AgentID: g.AgentID}, nil
}

// Revoke ends a grant at once. It leaves a tombstone for an id not issued yet,
// so an issue that arrives late cannot bring it back; revoking twice is fine.
func (s *Service) Revoke(ctx context.Context, id, project, run string) error {
	if len(id) == 0 || len(id) > 64 || !grantIDPattern.MatchString(id) || !valid(project, 128) || !valid(run, 128) {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tombstone := model.ExecutionGrant{ID: id, ProjectID: project, RunID: run, Status: StatusRevoked, ExpiresAt: s.now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tombstone).Error; err != nil {
			return err
		}
		var g model.ExecutionGrant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrConflict
			}
			return err
		}
		if g.ProjectID != project || g.RunID != run {
			return ErrConflict
		}
		if g.Status != StatusActive {
			return nil
		}
		return tx.Model(&g).Updates(map[string]any{"status": StatusRevoked, "secret_hash": ""}).Error
	})
}
