// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines interfaces for find_skills skill recall
package interfaces

import "context"

// ==================== Request and Response Structures ====================

// FindSkillsReq find_skills request
type FindSkillsReq struct {
	// Header Fields
	AccountID   string `json:"-" header:"x-account-id"`
	AccountType string `json:"-" header:"x-account-type"`

	// Body Parameters
	KnID string `json:"kn_id" validate:"required"`
	// ObjectTypeID and InstanceIdentities are accepted and ignored. Scope is now what the
	// knowledge network bound, which belongs to the branch rather than to one object type, so
	// answering differently per object type would invent a scope the data does not carry. They
	// stay in the contract — no longer required — so existing MCP callers keep working.
	ObjectTypeID       string                   `json:"object_type_id"`
	InstanceIdentities []map[string]interface{} `json:"instance_identities"`
	SkillQuery         string                   `json:"skill_query"`
	TopK               int                      `json:"top_k" default:"10" validate:"min=1,max=20"`
}

// FindSkillsResp find_skills response
type FindSkillsResp struct {
	Entries []*SkillItem `json:"entries"`
	Message string       `json:"message,omitempty"`
}

// SkillItem candidate skill metadata
type SkillItem struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ==================== Service Interface ====================

// IFindSkillsService find_skills service interface
type IFindSkillsService interface {
	FindSkills(ctx context.Context, req *FindSkillsReq) (*FindSkillsResp, error)
}
