// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
)

type refreshDatasets interface {
	kb.DatasetFileSnapshot
	kb.DatasetFileRecords
	kb.DatasetEntityRecords
}

// stagedSnapshot substitutes only the changed files' validated rows. An empty
// staged resource is genuinely empty; it never falls back to the old live rows.
// Unchanged file receipts and canonical entity history remain independently read.
type stagedSnapshot struct {
	refreshDatasets
	build *interfaces.KBBuild
	files map[int][]kb.DatasetWrite
}

func (s *stagedSnapshot) ReadFileRows(ctx context.Context, spec interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, guard func(context.Context) error) ([]map[string]any, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	for index, writes := range s.files {
		file := s.build.Files[index].File
		if file.KnowledgeBaseID != scope.KnowledgeBaseID || file.FileID != scope.FileID {
			continue
		}
		document, err := kbinstance.DocumentID(file.KnowledgeBaseID, file.FileID)
		if err != nil || scope.ContentVersionID != file.ContentVersionID || scope.DocumentID != document || scope.ModelFingerprint != s.build.ModelFingerprint {
			return nil, ErrInvalidManifest
		}
		rows := []map[string]any{}
		for _, write := range writes {
			if write.Spec.ResourceID == spec.ResourceID {
				rows = append(rows, write.Row)
			}
		}
		return rows, guard(ctx)
	}
	return s.refreshDatasets.ReadFileRows(ctx, spec, scope, guard)
}

// StageFusion previews the existing K5 engine against the complete staged view.
// It changes no file acknowledgement, canonical value or relation in storage.
func (s *Service) StageFusion(ctx context.Context, r *interfaces.KBRefreshRecord) (*StagedBatch, *kbinstance.PublishedFusionPlan, error) {
	staged, err := s.Stage(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	data, ok := s.Storage.(refreshDatasets)
	if !ok {
		return nil, nil, kb.ErrUnavailable
	}
	overlay := &stagedSnapshot{refreshDatasets: data, build: r.Pending.Build, files: staged.Files}
	now := r.Pending.EvaluationTime
	if now <= 0 {
		now = r.Pending.Build.CreateTime
	}
	plan, err := kbinstance.PreparePublishedFusion(ctx, overlay, s.Vega, r.Pending.Build, staged.Preparation.Model, now, staged.Preparation.Guard)
	if err != nil {
		return nil, nil, err
	}
	return staged, plan, nil
}
