// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package managedproxy owns the lifecycle of app identities that represent a
// knowledge network when downstream services enforce resource permissions.
package managedproxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const (
	ManagerBKN               = "bkn"
	ResourceKnowledgeNetwork = "knowledge_network"

	StatusActive    = "active"
	StatusDisabling = "disabling"
	StatusArchived  = "archived"
)

var (
	ErrInvalidManagedResource = errors.New("invalid managed resource")
	ErrManagedAccount         = errors.New("managed proxy account is protected")
	ErrInconsistentAccount    = errors.New("managed proxy account is inconsistent")
)

// Account is the lifecycle view returned to the BKN control plane. The two
// capability flags are invariants, not mutable account settings.
type Account struct {
	ProxyAccountID            string `json:"proxy_account_id"`
	AccountType               string `json:"account_type"`
	Name                      string `json:"name"`
	ManagedBy                 string `json:"managed_by"`
	ManagedResourceType       string `json:"managed_resource_type"`
	ManagedResourceID         string `json:"managed_resource_id"`
	LifecycleStatus           string `json:"lifecycle_status"`
	Enabled                   bool   `json:"enabled"`
	LoginEnabled              bool   `json:"login_enabled"`
	CredentialIssuanceEnabled bool   `json:"credential_issuance_enabled"`
	Version                   uint64 `json:"version"`
}

// CreateRequest identifies the environment-local resource that owns the app.
// ManagedBy is deliberately absent: this endpoint is BKN-specific and clients
// cannot turn it into a generic managed-account issuer.
type CreateRequest struct {
	ManagedResourceType string `json:"managed_resource_type"`
	ManagedResourceID   string `json:"managed_resource_id"`
	Name                string `json:"name"`
}

// Service persists managed identities and their one-to-one ownership mapping.
type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service { return &Service{db: db} }

// Create provisions the unique active proxy for a KN. Replaying the same
// request is idempotent and returns created=false with the existing identity.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Account, bool, error) {
	req.ManagedResourceType = strings.TrimSpace(req.ManagedResourceType)
	req.ManagedResourceID = strings.TrimSpace(req.ManagedResourceID)
	req.Name = strings.TrimSpace(req.Name)
	if req.ManagedResourceType != ResourceKnowledgeNetwork || req.ManagedResourceID == "" ||
		len(req.ManagedResourceID) > 128 || len(req.Name) > 255 {
		return nil, false, ErrInvalidManagedResource
	}

	if existing, err := s.getByManagedResource(ctx, req.ManagedResourceType, req.ManagedResourceID); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	proxyID, err := newID()
	if err != nil {
		return nil, false, fmt.Errorf("generate managed proxy id: %w", err)
	}
	if req.Name == "" {
		req.Name = "BKN proxy " + req.ManagedResourceID
		if len(req.Name) > 255 {
			req.Name = req.Name[:255]
		}
	}
	user := model.User{
		ID: proxyID, Account: "bkn-proxy-" + proxyID, Name: req.Name,
		Enabled: true, Source: model.SourceLocal, AccountType: model.AccountTypeApp,
		PasswordHash: "", MustChangePassword: false,
	}
	mapping := model.ManagedProxyAccount{
		ProxyAccountID: proxyID, ManagedBy: ManagerBKN,
		ManagedResourceType: req.ManagedResourceType, ManagedResourceID: req.ManagedResourceID,
		LifecycleStatus: StatusActive, Version: 1,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Let the composite database key arbitrate concurrent creates. The
		// mapping is inserted first inside the transaction so a losing request
		// never creates an orphan User row.
		if err := tx.Create(&mapping).Error; err != nil {
			return err
		}
		return tx.Create(&user).Error
	})
	if err == nil {
		return render(user, mapping), true, nil
	}

	// A concurrent request can win the unique mapping key after our first read.
	// Its transaction has committed before the conflicting insert returns, so
	// resolve the authoritative identity instead of surfacing a conflict.
	existing, readErr := s.getByManagedResource(ctx, req.ManagedResourceType, req.ManagedResourceID)
	if readErr == nil {
		return existing, false, nil
	}
	return nil, false, fmt.Errorf("create managed proxy: %w", err)
}

func (s *Service) Get(ctx context.Context, proxyAccountID string) (*Account, error) {
	return s.get(ctx, "proxy_account_id = ?", strings.TrimSpace(proxyAccountID))
}

func (s *Service) getByManagedResource(ctx context.Context, resourceType, resourceID string) (*Account, error) {
	return s.get(ctx, "managed_by = ? AND managed_resource_type = ? AND managed_resource_id = ?",
		ManagerBKN, resourceType, resourceID)
}

func (s *Service) get(ctx context.Context, query string, args ...any) (*Account, error) {
	var mapping model.ManagedProxyAccount
	if err := s.db.WithContext(ctx).Where(query, args...).First(&mapping).Error; err != nil {
		return nil, err
	}
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", mapping.ProxyAccountID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInconsistentAccount
		}
		return nil, err
	}
	if user.AccountType != model.AccountTypeApp || user.PasswordHash != "" ||
		mapping.ManagedBy != ManagerBKN || mapping.ManagedResourceType != ResourceKnowledgeNetwork {
		return nil, ErrInconsistentAccount
	}
	statusActive := mapping.LifecycleStatus == StatusActive
	if (!statusActive && mapping.LifecycleStatus != StatusDisabling && mapping.LifecycleStatus != StatusArchived) ||
		user.Enabled != statusActive {
		return nil, ErrInconsistentAccount
	}
	return render(user, mapping), nil
}

// Disable is idempotent and immediately makes the account fail the existing
// active-account guard used by every authorization decision endpoint.
func (s *Service) Disable(ctx context.Context, proxyAccountID string) (*Account, error) {
	return s.transition(ctx, proxyAccountID, StatusDisabling)
}

// Archive permanently keeps the identity disabled while preserving the row for
// historical audit joins.
func (s *Service) Archive(ctx context.Context, proxyAccountID string) (*Account, error) {
	return s.transition(ctx, proxyAccountID, StatusArchived)
}

func (s *Service) transition(ctx context.Context, proxyAccountID, target string) (*Account, error) {
	proxyAccountID = strings.TrimSpace(proxyAccountID)
	if proxyAccountID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var mapping model.ManagedProxyAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&mapping, "proxy_account_id = ?", proxyAccountID).Error; err != nil {
			return err
		}
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", proxyAccountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInconsistentAccount
			}
			return err
		}
		if user.AccountType != model.AccountTypeApp || user.PasswordHash != "" {
			return ErrInconsistentAccount
		}
		if user.Enabled {
			if err := tx.Model(&user).Update("enabled", false).Error; err != nil {
				return err
			}
		}
		if mapping.LifecycleStatus == StatusArchived || mapping.LifecycleStatus == target {
			return nil
		}
		return tx.Model(&mapping).Updates(map[string]any{
			"lifecycle_status": target,
			"version":          gorm.Expr("version + ?", 1),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, proxyAccountID)
}

// IsManaged reports whether an identity is protected by the managed lifecycle.
func IsManaged(ctx context.Context, db *gorm.DB, accountID string) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&model.ManagedProxyAccount{}).
		Where("proxy_account_id = ?", accountID).Count(&count).Error
	return count > 0, err
}

func render(user model.User, mapping model.ManagedProxyAccount) *Account {
	return &Account{
		ProxyAccountID: user.ID, AccountType: string(user.AccountType), Name: user.Name,
		ManagedBy: mapping.ManagedBy, ManagedResourceType: mapping.ManagedResourceType,
		ManagedResourceID: mapping.ManagedResourceID, LifecycleStatus: mapping.LifecycleStatus,
		Enabled: user.Enabled, LoginEnabled: false, CredentialIssuanceEnabled: false,
		Version: mapping.Version,
	}
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
