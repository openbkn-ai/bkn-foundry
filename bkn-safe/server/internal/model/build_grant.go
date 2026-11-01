// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package model

import "time"

// BuildGrant is an immutable delegated content scope, not a user credential or
// a network proxy grant. Revocation retains its private identity for cleanup.
type BuildGrant struct {
	ID               string `gorm:"primaryKey;size:64"`
	ProjectID        string `gorm:"size:128;not null;uniqueIndex:uidx_build_grant_job,priority:1"`
	BuildID          string `gorm:"size:128;not null;uniqueIndex:uidx_build_grant_job,priority:2"`
	PrincipalID      string `gorm:"size:64;index"`
	DelegatorID      string `gorm:"size:64"`
	ModelFingerprint string `gorm:"size:64"`
	ManifestHash     string `gorm:"size:64"`
	Revision         uint64 `gorm:"not null"`
	Status           string `gorm:"size:16;not null;index"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// BuildGrantVersion is the exact file/content-version membership of one grant.
type BuildGrantVersion struct {
	GrantID          string `gorm:"primaryKey;size:64"`
	KnowledgeBaseID  string `gorm:"primaryKey;size:128"`
	FileID           string `gorm:"primaryKey;size:128"`
	ContentVersionID string `gorm:"primaryKey;size:128"`
}
