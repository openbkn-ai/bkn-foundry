// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package model

import "time"

// ExecutionGrant is a short-lived credential one background execution acts with
// for the person who started it: one knowledge network, one Agent, one run. It
// resolves to the delegator, so every check downstream is that person's live
// permission; it holds no permission of its own. Only the secret's hash is kept.
// A revoked grant stays as a tombstone, so a late issue cannot restore it.
type ExecutionGrant struct {
	ID          string    `gorm:"primaryKey;size:64"`
	ProjectID   string    `gorm:"size:128;not null;uniqueIndex:uidx_execution_grant_run,priority:1"`
	RunID       string    `gorm:"size:128;not null;uniqueIndex:uidx_execution_grant_run,priority:2"`
	DelegatorID string    `gorm:"size:64"`
	KnID        string    `gorm:"size:128"`
	AgentID     string    `gorm:"size:128"`
	SecretHash  string    `gorm:"size:64"`
	ExpiresAt   time.Time `gorm:"index"`
	Status      string    `gorm:"size:16;not null;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
