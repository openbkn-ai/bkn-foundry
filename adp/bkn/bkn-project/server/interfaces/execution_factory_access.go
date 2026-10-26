// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// SKILL_STATUS_PUBLISHED is the status of a published Skill.
const SKILL_STATUS_PUBLISHED = "published"

// Import modes of the execution-factory impex API.
const (
	// IMPEX_MODE_CREATE refuses a toolbox whose id already exists.
	IMPEX_MODE_CREATE = "create"
	// IMPEX_MODE_UPSERT creates the toolbox or replaces the one with the same id.
	IMPEX_MODE_UPSERT = "upsert"
)

// ExecutionFactoryError is a non-2xx reply from execution-factory, kept whole
// so the caller can tell a refused permission from an unreachable service.
type ExecutionFactoryError struct {
	HTTPCode int
	httperrors.BaseError
}

func (e *ExecutionFactoryError) Error() string {
	return fmt.Sprintf("execution-factory returned HTTP %d: %s %s", e.HTTPCode, e.ErrorCode, e.Description)
}

// ExecutionFactoryAccess reads and copies the functions (toolboxes) and Skills
// a network uses, through the execution-factory operator-integration public
// API. token is the caller's Authorization header: execution-factory checks
// the caller's own permissions and makes the caller the owner of what it creates.
//
//go:generate mockgen -source ../interfaces/execution_factory_access.go -destination ../interfaces/mock/mock_execution_factory_access.go
type ExecutionFactoryAccess interface {
	// ExportToolbox returns the impex export of a toolbox: its tools, their
	// metadata, function code and dependencies, and the operators they use.
	ExportToolbox(ctx context.Context, token string, boxID string) ([]byte, error)
	// ImportToolbox imports an impex export in IMPEX_MODE_CREATE or IMPEX_MODE_UPSERT.
	// The toolbox and tool ids are taken from data.
	ImportToolbox(ctx context.Context, token string, data []byte, mode string) error
	// GetReleasedSkill returns the files of the published release of a Skill,
	// keyed by their path in the Skill package; SKILL.md is always among them.
	GetReleasedSkill(ctx context.Context, token string, skillID string) (map[string][]byte, error)
	// FindSkill returns the id and status of the Skill named exactly name and
	// visible to the caller: a published one if there is, else a draft
	// (unpublish or editing); "" when there is neither.
	FindSkill(ctx context.Context, token string, name string) (id string, status string, err error)
	// RegisterSkill registers a Skill package (a zip) and returns its new id.
	// The Skill is not published yet.
	RegisterSkill(ctx context.Context, token string, archive []byte) (string, error)
	// PublishSkill publishes the current version of a Skill.
	PublishSkill(ctx context.Context, token string, skillID string) error
}
