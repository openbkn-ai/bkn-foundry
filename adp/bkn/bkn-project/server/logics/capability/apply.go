// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Operations an ItemError reports.
const (
	OPERATION_EXPORT = "export"
	OPERATION_COPY   = "copy"
)

// ItemError names the function or Skill whose export or copy failed.
type ItemError struct {
	Kind      Kind
	ID        string
	Name      string
	Operation string
	Err       error
}

func (e *ItemError) Error() string {
	return fmt.Sprintf("%s %s %s (%s): %v", e.Operation, e.Kind, e.ID, e.Name, e.Err)
}

func (e *ItemError) Unwrap() error {
	return e.Err
}

// builtInRefusal ends the error code execution-factory replies with when it
// refuses to export a built-in toolbox (operator-integration
// infra/errors/error_code.go ErrExtCommonInternalComponentNotAllowed).
const builtInRefusal = ".CommonInternalComponentNotAllowed"

func isBuiltIn(err error) bool {
	var efaErr *interfaces.ExecutionFactoryError
	return errors.As(err, &efaErr) && efaErr.HTTPCode == http.StatusForbidden &&
		strings.HasSuffix(efaErr.ErrorCode, builtInRefusal)
}

func isNotFound(err error) bool {
	var efaErr *interfaces.ExecutionFactoryError
	return errors.As(err, &efaErr) && efaErr.HTTPCode == http.StatusNotFound
}

// ExportFunction reads the live toolbox boxID.
func ExportFunction(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string, boxID string) (*Item, error) {
	export, err := efa.ExportToolbox(ctx, token, boxID)
	if err == nil {
		var it *Item
		if it, err = NewFunction(export); err == nil && it.ID != boxID {
			err = fmt.Errorf("the export of toolbox %s holds toolbox %s", boxID, it.ID)
		}
		if err == nil {
			return it, nil
		}
	}
	return nil, &ItemError{Kind: KIND_FUNCTION, ID: boxID, Operation: OPERATION_EXPORT, Err: err}
}

// ExportSkill reads the released version of Skill skillID, not its draft.
func ExportSkill(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string, skillID string) (*Item, error) {
	files, err := efa.GetReleasedSkill(ctx, token, skillID)
	if err == nil {
		var it *Item
		if it, err = NewSkill(skillID, files); err == nil {
			return it, nil
		}
	}
	return nil, &ItemError{Kind: KIND_SKILL, ID: skillID, Operation: OPERATION_EXPORT, Err: err}
}

// Export reads the live state of the toolboxes and Skills given by id.
func Export(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string,
	boxIDs []string, skillIDs []string) ([]*Item, error) {
	items := make([]*Item, 0, len(boxIDs)+len(skillIDs))
	for _, id := range boxIDs {
		it, err := ExportFunction(ctx, efa, token, id)
		if isBuiltIn(err) {
			// A built-in toolbox belongs to the platform, not to the network:
			// it is reused as it is and not kept in git.
			otellog.LogInfo(ctx, fmt.Sprintf("Toolbox %s is built in and is not followed", id))
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	for _, id := range skillIDs {
		it, err := ExportSkill(ctx, efa, token, id)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// ExportManifest reads the live state of every item the manifest lists. An
// item that no longer exists, a deleted toolbox or a Skill taken offline, is
// left out, so it shows up as removed rather than failing every later check.
func ExportManifest(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string,
	m *Manifest) ([]*Item, error) {
	items := make([]*Item, 0, len(m.Functions)+len(m.Skills))
	for _, e := range m.Functions {
		boxItems, err := Export(ctx, efa, token, []string{e.ID}, nil)
		if isNotFound(err) {
			otellog.LogInfo(ctx, fmt.Sprintf("Toolbox %s of release.yaml no longer exists", e.ID))
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, boxItems...)
	}
	for _, e := range m.Skills {
		it, err := ExportSkill(ctx, efa, token, e.ID)
		if isNotFound(err) {
			otellog.LogInfo(ctx, fmt.Sprintf("Skill %s of release.yaml is no longer released", e.ID))
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// Apply makes the copies the steps call for and returns them, with the items
// the new network uses as they are stored for its release: a reused item as
// it is, a copy as execution-factory exports it after the copy.
//
// Copies are idempotent, so a retried release does not pile them up: a
// toolbox copy has an id derived from the original and the new network and
// is imported with upsert, and a Skill copy already published under its name
// is taken instead of registering another one.
func Apply(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string, steps []*Step,
	oldNetworkID string, newNetworkID string) ([]*Copy, []*Item, error) {
	copies := []*Copy{}
	items := make([]*Item, 0, len(steps))
	for _, step := range steps {
		it := step.Item
		if !step.Copy {
			items = append(items, it)
			continue
		}
		var c *Copy
		var copied *Item
		var err error
		if it.Kind == KIND_FUNCTION {
			c, copied, err = copyFunction(ctx, efa, token, it, oldNetworkID, newNetworkID)
		} else {
			c, copied, err = copySkill(ctx, efa, token, it, oldNetworkID, newNetworkID)
		}
		if err != nil {
			return nil, nil, &ItemError{Kind: it.Kind, ID: it.ID, Name: it.Name, Operation: OPERATION_COPY, Err: err}
		}
		otellog.LogInfo(ctx, fmt.Sprintf("Copied %s %s to %s for network %s, reasons %v",
			it.Kind, it.ID, c.NewID, newNetworkID, step.Reasons))
		copies = append(copies, c)
		items = append(items, copied)
	}
	return copies, items, nil
}

// Preview returns the copies the steps call for without making them, so the
// references a release must move can be checked before anything is created. A
// function copy gets the ids Apply gives it; a Skill copy gets a stand-in id,
// since registration assigns the real one.
func Preview(steps []*Step, oldNetworkID string, newNetworkID string) ([]*Copy, error) {
	copies := []*Copy{}
	for _, step := range steps {
		if !step.Copy {
			continue
		}
		it := step.Item
		var c *Copy
		var err error
		if it.Kind == KIND_FUNCTION {
			_, c, err = CopyFunction(it, oldNetworkID, newNetworkID)
		} else if _, c, err = CopySkill(it, oldNetworkID, newNetworkID); err == nil {
			c.NewID = copyID(it.ID, newNetworkID)
		}
		if err != nil {
			return nil, &ItemError{Kind: it.Kind, ID: it.ID, Name: it.Name, Operation: OPERATION_COPY, Err: err}
		}
		copies = append(copies, c)
	}
	return copies, nil
}

func copyFunction(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string, it *Item,
	oldNetworkID string, newNetworkID string) (*Copy, *Item, error) {
	payload, c, err := CopyFunction(it, oldNetworkID, newNetworkID)
	if err != nil {
		return nil, nil, err
	}
	if err := efa.ImportToolbox(ctx, token, payload, interfaces.IMPEX_MODE_UPSERT); err != nil {
		return nil, nil, err
	}
	copied, err := ExportFunction(ctx, efa, token, c.NewID)
	if err != nil {
		return nil, nil, err
	}
	return c, copied, nil
}

func copySkill(ctx context.Context, efa interfaces.ExecutionFactoryAccess, token string, it *Item,
	oldNetworkID string, newNetworkID string) (*Copy, *Item, error) {
	files, c, err := CopySkill(it, oldNetworkID, newNetworkID)
	if err != nil {
		return nil, nil, err
	}
	if c.NewID, err = efa.FindPublishedSkill(ctx, token, c.NewName); err != nil {
		return nil, nil, err
	}
	if c.NewID == "" {
		archive, err := SkillArchive(files)
		if err != nil {
			return nil, nil, err
		}
		if c.NewID, err = efa.RegisterSkill(ctx, token, archive); err != nil {
			return nil, nil, err
		}
		if err := efa.PublishSkill(ctx, token, c.NewID); err != nil {
			return nil, nil, err
		}
	}
	copied, err := ExportSkill(ctx, efa, token, c.NewID)
	if err != nil {
		return nil, nil, err
	}
	return c, copied, nil
}
