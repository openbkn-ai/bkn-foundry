// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package identity exposes the directory row contract used by identity extensions.
// Extensions own their companion tables and never migrate or replace core tables.
package identity

import (
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

type User = model.User
type Department = model.Department
type UserDepartment = model.UserDepartment
type Source = model.Source

const AccountTypeOther = model.AccountTypeOther

// ErrExternalIdentity rejects local writes to fields managed by an identity provider.
var ErrExternalIdentity = errors.New("identity fields are managed by an external provider")

// Cleanup supplies companion cleanup and origin metadata to the core directory.
// Delete hooks join the core transaction; errors roll the deletion back. Registration
// is per database and happens during assembly, before requests are served.
type Cleanup struct {
	// DepartmentSources supplies read-only origin metadata in a single batch.
	DepartmentSources func(tx *gorm.DB, ids []string) (map[string]string, error)
	User              func(tx *gorm.DB, id string) error
	Department        func(tx *gorm.DB, id string) error
}

func (c Cleanup) Name() string              { return "identity-companion-cleanup" }
func (c Cleanup) Initialize(*gorm.DB) error { return nil }

func RegisterCleanup(db *gorm.DB, callbacks Cleanup) error { return db.Use(callbacks) }

func CleanupUser(tx *gorm.DB, id string) error {
	if c, ok := tx.Plugins[(Cleanup{}).Name()].(Cleanup); ok && c.User != nil {
		return c.User(tx, id)
	}
	return nil
}
func CleanupDepartment(tx *gorm.DB, id string) error {
	if c, ok := tx.Plugins[(Cleanup{}).Name()].(Cleanup); ok && c.Department != nil {
		return c.Department(tx, id)
	}
	return nil
}

// DepartmentSources returns origins owned by the installed identity extension.
func DepartmentSources(db *gorm.DB, ids []string) (map[string]string, error) {
	if c, ok := db.Plugins[(Cleanup{}).Name()].(Cleanup); ok && c.DepartmentSources != nil && len(ids) > 0 {
		return c.DepartmentSources(db, ids)
	}
	return map[string]string{}, nil
}
