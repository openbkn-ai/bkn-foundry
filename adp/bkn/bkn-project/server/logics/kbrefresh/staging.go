// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
)

// StagedBatch is bounded, private memory only. Agent already persists the actual
// task output, so recovery can reconstruct this exact set without another model
// invocation or a parallel instance store. Nothing here is a public status DTO.
type StagedBatch struct {
	Files       map[int][]kb.DatasetWrite
	Writes      []kb.DatasetWrite
	Digest      string
	Preparation *Preparation
}

// Stage rechecks every changed task, locked source version and full write set
// before any live write. Unchanged files reuse their actual receipts and require
// neither task access under an obsolete build identity nor another inference.
func (s *Service) Stage(ctx context.Context, r *interfaces.KBRefreshRecord) (*StagedBatch, error) {
	if r == nil || r.Pending == nil || r.Pending.Build == nil {
		return nil, ErrInvalidManifest
	}
	prepared, err := s.resume(ctx, r)
	if err != nil {
		return nil, err
	}
	build := r.Pending.Build
	result := &StagedBatch{Files: map[int][]kb.DatasetWrite{}, Writes: []kb.DatasetWrite{}, Preparation: prepared}
	for _, index := range r.Pending.Changed {
		if index < 0 || index >= len(build.Files) || s.Tasks == nil {
			return nil, ErrInvalidManifest
		}
		file := build.Files[index]
		if file.State != "validated" || len(file.RowsDigest) != 64 || file.AgentTaskID == "" || file.AgentVersion == nil || file.PromptVersion == nil {
			return nil, ErrInvalidManifest
		}
		if err := prepared.Guard(ctx); err != nil {
			return nil, err
		}
		task, err := s.Tasks.Poll(ctx, build.Owner, file.AgentTaskID)
		if err != nil {
			return nil, err
		}
		if task == nil || task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED || task.TaskID != file.AgentTaskID || task.AgentID != kb.DocumentExtractionAgentID || task.AgentVersion == nil || *task.AgentVersion != *file.AgentVersion || task.PromptVersion == nil || *task.PromptVersion != *file.PromptVersion || task.ModelID != file.ModelID || task.ModelID != build.ModelID || task.Model != build.Model {
			return nil, ErrInvalidManifest
		}
		ref := kb.SampleVersion{KnowledgeBaseID: file.File.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: file.File.FileID, ContentVersionID: file.File.ContentVersionID}}
		input, err := kbinstance.ReadFileInput(ctx, prepared.Source, ref)
		if err != nil {
			return nil, err
		}
		writes, err := kbinstance.MaterializeFile(build, index, prepared.Model, input, task)
		if err != nil {
			return nil, err
		}
		digest, err := kbinstance.ValidateWriteSet(build, prepared.Model, writes)
		if err != nil || digest != file.RowsDigest || file.RowsWritten < 0 || file.RowsWritten > int64(len(writes)) {
			return nil, ErrInvalidManifest
		}
		result.Files[index] = writes
		result.Writes = append(result.Writes, writes...)
		if len(result.Writes) > 100000 {
			return nil, ErrInvalidManifest
		}
	}
	digest, err := kbinstance.ValidateWriteSet(build, prepared.Model, result.Writes)
	if err != nil {
		return nil, err
	}
	if r.Pending.RowsDigest != "" && r.Pending.RowsDigest != digest {
		return nil, ErrInvalidManifest
	}
	result.Digest = digest
	if err := prepared.Guard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
