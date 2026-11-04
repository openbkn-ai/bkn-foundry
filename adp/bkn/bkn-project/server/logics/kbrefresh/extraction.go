// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
	"slices"
	"strings"
	"time"
)

func (s *Service) saveRecord(ctx context.Context, r *interfaces.KBRefreshRecord) error {
	revision := r.Revision
	r.Revision++
	r.UpdateTime = max(time.Now().UnixMilli(), r.UpdateTime+1)
	until := r.UpdateTime + refreshLease.Milliseconds()
	ok, err := s.Journal.Save(ctx, r, revision, r.LeaseID, r.UpdateTime, until)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLostLease
	}
	r.LeaseUntil = until
	return nil
}
func (s *Service) attention(ctx context.Context, r *interfaces.KBRefreshRecord, reason string) error {
	// Once application may have written data, preserve its exact phase and
	// immutable intent. A failed guard never authorizes a write; the worker
	// checks it again after backoff and resumes only the same validated plan.
	if r.Pending != nil && r.Pending.ApplicationDigest != "" {
		if r.State != "applying" && r.State != "finishing" {
			return ErrInvalidManifest
		}
		r.Reason = reason
		r.NextCheck = time.Now().Add(refreshRetry).UnixMilli()
		return s.saveRecord(ctx, r)
	}
	r.State, r.Reason = "attention", reason
	return s.saveRecord(ctx, r)
}
func (s *Service) resume(ctx context.Context, r *interfaces.KBRefreshRecord) (*Preparation, error) {
	if r.Pending == nil || r.Pending.Build == nil {
		return nil, ErrInvalidManifest
	}
	guard := func(ctx context.Context) error {
		if s.Enabled != nil && !s.Enabled() {
			return kb.ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		latest, err := s.Journal.Get(ctx, r.LineageID)
		if err != nil {
			return err
		}
		if latest == nil || latest.Revision != r.Revision || latest.LeaseID != r.LeaseID || latest.LeaseUntil <= time.Now().UnixMilli() {
			return ErrLostLease
		}
		current, err := Resolve(ctx, r.LineageID, s.Releases, s.Publications)
		if err != nil {
			return err
		}
		if (s.ExpectedNetwork != "" && current.NetworkID != s.ExpectedNetwork) || current.NetworkID != r.NetworkID || current.Revision != r.Pending.PublicationRevision || current.RuntimeDigest != r.Pending.RuntimeDigest || current.ModelFingerprint != r.Pending.Build.ModelFingerprint || current.GenerationID != r.Pending.Build.GenerationID || current.Publisher != r.Pending.Build.Owner {
			return ErrNoPublishedTarget
		}
		return nil
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	publication, err := Resolve(ctx, r.LineageID, s.Releases, s.Publications)
	if err != nil {
		return nil, err
	}
	if s.Sources == nil {
		return nil, kb.ErrUnavailable
	}
	source, err := s.Sources.OpenPublished(ctx, publication, guard)
	if err != nil {
		return nil, err
	}
	model, err := kbpublication.LoadPublishedModel(ctx, s.Storage, publication)
	if err != nil {
		return nil, err
	}
	live := func(ctx context.Context) error {
		if err := guard(ctx); err != nil {
			return err
		}
		if err := source.Check(ctx); err != nil {
			return err
		}
		if s.Network != nil {
			return s.Network.CheckPublishedBindings(ctx, publication, model)
		}
		return nil
	}
	if err := live(ctx); err != nil {
		return nil, err
	}
	return &Preparation{Publication: publication, Model: model, Source: source, Plan: &ManifestPlan{Build: r.Pending.Build, Changed: r.Pending.Changed, Removed: r.Pending.Removed}, Cursors: r.Pending.Cursors, Guard: live}, nil
}

// ExtractStep advances one durable action and performs no live Dataset writes.
// All changed files must be validated before the batch becomes ready to apply.
// Recovery polls acknowledged task ids and never replays a submitting intent.
func (s *Service) ExtractStep(ctx context.Context, lineage, lease string) error {
	if s.Journal == nil {
		return kb.ErrUnavailable
	}
	now := time.Now().UnixMilli()
	r, err := s.Journal.Claim(ctx, lineage, lease, now, now+refreshLease.Milliseconds())
	if err != nil || r == nil {
		return err
	}
	if r.State != "running" {
		return nil
	}
	if !approvedBatch(r) {
		return s.attention(ctx, r, "batch_review_required")
	}
	prepared, err := s.resume(ctx, r)
	if err != nil {
		if err == ErrLostLease {
			return err
		}
		return s.attention(ctx, r, "source_or_publication_changed")
	}
	build := r.Pending.Build
	if build.State == "preparing" {
		for _, hold := range r.Pending.Holds {
			if err := prepared.Guard(ctx); err != nil {
				return s.attention(ctx, r, "source_access_changed")
			}
			refs := []kb.VersionRef{}
			for _, step := range build.Files {
				if step.File.KnowledgeBaseID == hold.KnowledgeBaseID && slices.Contains(hold.VersionIDs, step.File.ContentVersionID) {
					refs = append(refs, kb.VersionRef{FileID: step.File.FileID, ContentVersionID: step.File.ContentVersionID})
				}
			}
			if err := prepared.Source.Hold(ctx, kb.SourceScope{NetworkID: r.NetworkID, KnowledgeBaseID: hold.KnowledgeBaseID}, kb.HoldRequest{ID: hold.ID, Versions: refs}); err != nil {
				return s.attention(ctx, r, "source_hold_unavailable")
			}
		}
		build.State = "queued"
		return s.saveRecord(ctx, r)
	}
	for _, index := range r.Pending.Changed {
		file := &build.Files[index]
		if file.State == "validated" {
			continue
		}
		if file.State == "submitting" {
			next, err := kbinstance.InterruptFileSubmission(build, index, time.Now().UnixMilli())
			if err != nil {
				return err
			}
			r.Pending.Build = next
			return s.attention(ctx, r, "submission_unknown")
		}
		if s.Tasks == nil {
			return s.attention(ctx, r, "extraction_unavailable")
		}
		if file.State == "pending" || file.State == "reading" {
			ref := kb.SampleVersion{KnowledgeBaseID: file.File.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: file.File.FileID, ContentVersionID: file.File.ContentVersionID}}
			input, err := kbinstance.ReadFileInput(ctx, prepared.Source, ref)
			if err != nil {
				return s.attention(ctx, r, "source_unavailable")
			}
			hasText := false
			for _, text := range input.Text {
				hasText = hasText || strings.TrimSpace(text) != ""
			}
			if !hasText {
				return s.attention(ctx, r, "unsupported_content")
			}
			digest, err := kbinstance.FileInputDigest(ref, input)
			if err != nil {
				return s.attention(ctx, r, "invalid_evidence")
			}
			file.InputDigest = digest
			next, err := kbinstance.PrepareFileSubmission(build, index, time.Now().UnixMilli())
			if err != nil {
				return err
			}
			r.Pending.Build = next
			if err := s.saveRecord(ctx, r); err != nil {
				return err
			}
			if err := prepared.Guard(ctx); err != nil {
				// This process knows Start was never attempted; do not manufacture an
				// uncertain task solely because a pre-submission permission check failed.
				r.Pending.Build.Files[index].State = "pending"
				return s.attention(ctx, r, "source_access_changed")
			}
			task, err := s.Tasks.Start(ctx, next.Owner, next, index, prepared.Model, input)
			if err != nil || task == nil {
				interrupted, err := kbinstance.InterruptFileSubmission(next, index, time.Now().UnixMilli())
				if err != nil {
					return err
				}
				r.Pending.Build = interrupted
				return s.attention(ctx, r, "submission_unknown")
			}
			attached, err := kbinstance.AttachFileTask(next, index, kb.DocumentExtractionAgentID, task, time.Now().UnixMilli())
			if err != nil {
				if task.TaskID != "" && len(task.TaskID) <= 128 {
					next.Files[index].AgentTaskID = task.TaskID
					next.Files[index].State = "failed"
				}
				return s.attention(ctx, r, "execution_changed")
			}
			r.Pending.Build = attached
			if attached.State == "attention" {
				return s.attention(ctx, r, "extraction_failed")
			}
			return s.saveRecord(ctx, r)
		}
		if file.State != "waiting" && file.State != "validating" {
			return s.attention(ctx, r, "invalid_file_state")
		}
		if err := prepared.Guard(ctx); err != nil {
			return s.attention(ctx, r, "source_access_changed")
		}
		task, err := s.Tasks.Poll(ctx, build.Owner, file.AgentTaskID)
		if err != nil || task == nil {
			return s.attention(ctx, r, "task_unavailable")
		}
		attached, err := kbinstance.AttachFileTask(build, index, kb.DocumentExtractionAgentID, task, time.Now().UnixMilli())
		if err != nil {
			return s.attention(ctx, r, "execution_changed")
		}
		r.Pending.Build = attached
		if attached.State == "attention" {
			return s.attention(ctx, r, "extraction_failed")
		}
		if task.Status == interfaces.BKN_AGENT_TASK_SUCCEEDED {
			if task.PromptVersion == nil || *task.PromptVersion < 1 || task.ModelID == "" {
				return s.attention(ctx, r, "execution_unknown")
			}
			// Keep the completed task's actual optional name, including absence.
			attached.Files[index].Model = task.Model
			ref := kb.SampleVersion{KnowledgeBaseID: file.File.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: file.File.FileID, ContentVersionID: file.File.ContentVersionID}}
			input, err := kbinstance.ReadFileInput(ctx, prepared.Source, ref)
			if err != nil {
				return s.attention(ctx, r, "source_unavailable")
			}
			writes, err := kbinstance.MaterializeFile(attached, index, prepared.Model, input, task)
			if err != nil {
				return s.attention(ctx, r, "invalid_extraction")
			}
			digest, err := kbinstance.ValidateWriteSet(attached, prepared.Model, writes)
			if err != nil {
				return s.attention(ctx, r, "invalid_extraction")
			}
			attached.Files[index].State = "validated"
			attached.Files[index].RowsDigest = digest
			attached.Files[index].RowsWritten = 0
		}
		return s.saveRecord(ctx, r)
	}
	staged, err := s.Stage(ctx, r)
	if err != nil {
		return s.attention(ctx, r, "write_set_changed")
	}
	r.Pending.RowsDigest = staged.Digest
	r.State = "ready"
	return s.saveRecord(ctx, r)
}
