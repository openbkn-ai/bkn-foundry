// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

var ErrInvalidBuild = errors.New("document build does not match confirmed model and source scope")

type FileIdentityError struct{ Name string }

func (*FileIdentityError) Error() string { return "file business number requires human confirmation" }

var ErrAcceptanceRequired = errors.New("acceptance review is required before a full build")

// PlanFileBuild snapshots only exact source identities and process decisions.
// It authorizes nothing: the controller must atomically persist this intent
// under current project/source/model guards before asking Safe for delegation.
// Full-build metric/scale approval belongs to that controller as well.
func PlanFileBuild(id, network, mode string, agentVersion int, draft *interfaces.Draft, selection *interfaces.KBSelection) (*interfaces.KBBuild, error) {
	if strings.TrimSpace(network) == "" || strings.TrimSpace(id) == "" || agentVersion < 1 || draft == nil || selection == nil || draft.Status != interfaces.DRAFT_STATUS_READY || draft.UpdateTime < 1 || draft.Items.Confirmation == nil || draft.Items.Confirmation.UpdateTime != draft.UpdateTime || draft.Items.Confirmation.Commit == "" || selection.State != "active" || selection.ProjectID != draft.ProjectID || selection.ID == "" || selection.Revision < 1 || selection.Owner.ID == "" || selection.Owner.Type != "user" {
		return nil, ErrInvalidBuild
	}
	if mode != "debug" && mode != "acceptance" && mode != "full" {
		return nil, ErrInvalidBuild
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil {
		return nil, ErrInvalidBuild
	}
	var generation *interfaces.DraftDatasetPlan
	for i := len(draft.Items.DatasetPlans) - 1; i >= 0; i-- {
		plan := draft.Items.DatasetPlans[i]
		if plan != nil && plan.ModelFingerprint == fingerprint && plan.SelectionID == selection.ID && plan.SelectionRevision == selection.Revision {
			generation = plan
			break
		}
	}
	if generation == nil || generation.State != "ready" || generation.Owner != selection.Owner {
		return nil, ErrInvalidBuild
	}
	for _, spec := range generation.Datasets {
		if !spec.Ready {
			return nil, ErrInvalidBuild
		}
	}
	bound, err := kbdataset.BindPreview(draft, generation)
	if err != nil {
		return nil, ErrInvalidBuild
	}
	for i, object := range draft.Items.ObjectTypes {
		if object.Adopted && object.Document != nil && object.Document.Role == "file" {
			if _, err := kbconcept.FileIdentityProperty(draft.Items, object); err != nil {
				return nil, &FileIdentityError{Name: object.Name}
			}
		}
		if object.Adopted && object.Document != nil && object.Table != bound.Items.ObjectTypes[i].Table {
			return nil, ErrInvalidBuild
		}
	}
	files := []interfaces.KBFileBuild{}
	seen := map[string]bool{}
	for _, file := range selection.Files {
		key := file.Key()
		if file.KnowledgeBaseID == "" || file.FileID == "" || file.ContentVersionID == "" || seen[key] || (file.SampleRole != "" && file.SampleRole != "debug" && file.SampleRole != "acceptance") {
			return nil, ErrInvalidBuild
		}
		seen[key] = true
		if mode != "full" && file.SampleRole != mode {
			continue
		}
		files = append(files, interfaces.KBFileBuild{File: file, State: "pending"})
	}
	if len(files) == 0 || len(files) > 1000 {
		return nil, ErrInvalidBuild
	}
	slices.SortFunc(files, func(a, b interfaces.KBFileBuild) int { return strings.Compare(a.File.Key(), b.File.Key()) })
	now := time.Now().UnixMilli()
	return &interfaces.KBBuild{ID: id, NetworkID: network, ProjectID: draft.ProjectID, Owner: selection.Owner, Mode: mode, State: "preparing", Revision: 1, SelectionID: selection.ID, SelectionRevision: selection.Revision, ModelFingerprint: fingerprint, GenerationID: generation.ID, DraftUpdateTime: draft.UpdateTime, DefinitionCommit: draft.Items.Confirmation.Commit, AgentVersion: agentVersion, Files: files, CreateTime: now, UpdateTime: now}, nil
}
